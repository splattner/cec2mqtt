package main

import (
	"sync"
	"testing"

	"github.com/RobertMe/gocec"
)

type fakeCec struct {
	mux      sync.Mutex
	address  gocec.LogicalAddress
	messages []gocec.Message
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
	return &HomeAssistantBridge{
		discoveryPrefix: "homeassistant",
		mqtt:            mqtt,
		config:          &Config{Mqtt: MqttConfig{BaseTopic: "cec2mqtt"}},
	}
}
