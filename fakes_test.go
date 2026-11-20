package main

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

// waitFor polls condition until it's true, for tests running on the real clock.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type fakeCec struct {
	mux      sync.Mutex
	address  gocec.LogicalAddress
	messages []gocec.Message

	// powerStatus is what libcec reports per device, unknown when not set.
	powerStatus map[gocec.LogicalAddress]gocec.PowerStatus
	poweredOn   []gocec.LogicalAddress
	standby     []gocec.LogicalAddress

	activeSource         gocec.LogicalAddress
	activeSourceRequests int

	handlers map[gocec.Opcode][]MessageReceivedHandler

	// scan is called by Scan, when set.
	scan  func()
	scans int
}

func (cec *fakeCec) Scan() {
	cec.mux.Lock()
	cec.scans++
	scan := cec.scan
	cec.mux.Unlock()

	if scan != nil {
		scan()
	}
}

func (cec *fakeCec) AdapterPath() string {
	return "/dev/cec0"
}

func (cec *fakeCec) RegisterMessageHandler(handler MessageReceivedHandler, opcodes ...gocec.Opcode) {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	if cec.handlers == nil {
		cec.handlers = make(map[gocec.Opcode][]MessageReceivedHandler)
	}
	for _, opcode := range opcodes {
		cec.handlers[opcode] = append(cec.handlers[opcode], handler)
	}
}

// receive simulates a message received from a device.
func (cec *fakeCec) receive(message gocec.Message) {
	cec.mux.Lock()
	handlers := append([]MessageReceivedHandler(nil), cec.handlers[message.Opcode()]...)
	cec.mux.Unlock()

	for _, handler := range handlers {
		handler(message)
	}
}

func (cec *fakeCec) PowerOn(address gocec.LogicalAddress) {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	cec.poweredOn = append(cec.poweredOn, address)
}

func (cec *fakeCec) Standby(address gocec.LogicalAddress) {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	cec.standby = append(cec.standby, address)
}

func (cec *fakeCec) PowerStatus(address gocec.LogicalAddress) gocec.PowerStatus {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	if status, ok := cec.powerStatus[address]; ok {
		return status
	}
	return gocec.PowerStatusUnknown
}

func (cec *fakeCec) setPowerStatus(address gocec.LogicalAddress, status gocec.PowerStatus) {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	if cec.powerStatus == nil {
		cec.powerStatus = make(map[gocec.LogicalAddress]gocec.PowerStatus)
	}
	cec.powerStatus[address] = status
}

func (cec *fakeCec) ActiveSource() gocec.LogicalAddress {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	cec.activeSourceRequests++
	return cec.activeSource
}

func (cec *fakeCec) Transmit(message gocec.Message) {
	cec.mux.Lock()
	defer cec.mux.Unlock()
	cec.messages = append(cec.messages, message)
}

func (cec *fakeCec) AdapterAddress() gocec.LogicalAddress {
	return cec.address
}

// sent returns the transmitted messages, optionally only those with one of the opcodes.
func (cec *fakeCec) sent(opcodes ...gocec.Opcode) []gocec.Message {
	cec.mux.Lock()
	defer cec.mux.Unlock()

	var messages []gocec.Message
	for _, message := range cec.messages {
		if len(opcodes) == 0 {
			messages = append(messages, message)
			continue
		}
		for _, opcode := range opcodes {
			if message.Opcode() == opcode {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

type published struct {
	topic    string
	retained bool
	payload  string
}

type fakeMqtt struct {
	mux               sync.Mutex
	published         []published
	subscriptions     map[string]MessageHandler
	connectedHandlers []MqttConnectedHandler
}

func newFakeMqtt() *fakeMqtt {
	return &fakeMqtt{subscriptions: make(map[string]MessageHandler)}
}

func (m *fakeMqtt) BuildTopic(device *Device, suffix string) string {
	return "cec2mqtt/" + device.Config.MqttTopic + "/" + suffix
}

func (m *fakeMqtt) Publish(topic string, qos byte, retained bool, payload interface{}) {
	m.mux.Lock()
	defer m.mux.Unlock()

	var text string
	switch p := payload.(type) {
	case string:
		text = p
	case []byte:
		text = string(p)
	}
	m.published = append(m.published, published{topic: topic, retained: retained, payload: text})
}

func (m *fakeMqtt) Subscribe(topic string, qos byte, callback MessageHandler) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.subscriptions[topic] = callback
}

func (m *fakeMqtt) RegisterConnectedHandler(handler MqttConnectedHandler) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.connectedHandlers = append(m.connectedHandlers, handler)
}

// deliver simulates an incoming MQTT message.
func (m *fakeMqtt) deliver(t *testing.T, topic string, payload string) {
	t.Helper()
	m.mux.Lock()
	callback, ok := m.subscriptions[topic]
	m.mux.Unlock()
	if !ok {
		t.Fatalf("no subscription for topic %q", topic)
	}
	callback([]byte(payload))
}

// messages returns the payloads published on the topic.
func (m *fakeMqtt) messages(topic string) []string {
	m.mux.Lock()
	defer m.mux.Unlock()

	var payloads []string
	for _, p := range m.published {
		if p.topic == topic {
			payloads = append(payloads, p.payload)
		}
	}
	return payloads
}

func (m *fakeMqtt) reset() {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.published = nil
}

// addTestDevice adds a device to the registry like it was found on the CEC bus.
func addTestDevice(t *testing.T, registry *DeviceRegistry, address gocec.LogicalAddress, physicalAddress gocec.PhysicalAddress, osd string) *Device {
	t.Helper()
	description := &CecDeviceDescription{
		logicalAddress:  address,
		physicalAddress: physicalAddress,
		vendor:          gocec.VendorSamsung,
		OSD:             osd,
	}
	device := registry.GetByCecDevice(address, func() *CecDeviceDescription { return description })
	if device == nil {
		t.Fatalf("failed to add device %s", osd)
	}
	return device
}

func newTestHomeAssistantBridge(mqtt mqttClient) *HomeAssistantBridge {
	config := &Config{
		Mqtt:          MqttConfig{BaseTopic: "cec2mqtt"},
		HomeAssistant: HomeAssistantConfig{Enable: true, DiscoveryPrefix: "homeassistant"},
	}
	bridge := newHomeAssistantBridge(config, mqtt)
	bridge.publishDelay = 0 // publish synchronously
	return bridge
}

// discovery returns the latest device discovery message of the device and its
// components, or nil when none was published.
func (m *fakeMqtt) discovery(t *testing.T, deviceId string) (map[string]any, map[string]map[string]any) {
	t.Helper()
	messages := m.messages("homeassistant/device/" + deviceId + "/config")
	if len(messages) == 0 {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(messages[len(messages)-1]), &payload); err != nil {
		t.Fatal(err)
	}

	components := make(map[string]map[string]any)
	raw, _ := payload["components"].(map[string]any)
	for id, config := range raw {
		components[id], _ = config.(map[string]any)
	}
	return payload, components
}
