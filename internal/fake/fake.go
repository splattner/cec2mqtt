// Package fake provides test doubles for the CEC adapter and the MQTT client,
// so the bridges can be tested without hardware or a broker.
package fake

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

// WaitFor polls condition until it's true, for tests running on the real clock.
func WaitFor(t *testing.T, what string, condition func() bool) {
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

// NewRegistry returns an empty device registry in a temporary directory.
func NewRegistry(t *testing.T) *device.Registry {
	t.Helper()
	return device.NewRegistry(t.TempDir() + "/")
}

// AddDevice adds a device to the registry like it was found on the CEC bus.
func AddDevice(t *testing.T, registry *device.Registry, address gocec.LogicalAddress, physicalAddress gocec.PhysicalAddress, osd string) *device.Device {
	t.Helper()
	description := &device.Description{
		LogicalAddress:  address,
		PhysicalAddress: physicalAddress,
		Vendor:          gocec.VendorSamsung,
		OSD:             osd,
	}
	d := registry.GetByCecDevice(address, func() *device.Description { return description })
	if d == nil {
		t.Fatalf("failed to add device %s", osd)
	}
	return d
}

// Cec is a CEC adapter which records what's sent, and delivers messages to
// the registered handlers with Receive. Lock Mux to read the fields while the
// code under test runs.
type Cec struct {
	Mux sync.Mutex

	// Address is the logical address of the adapter.
	Address gocec.LogicalAddress
	// Transmitted holds all transmitted messages.
	Transmitted []gocec.Message

	PowerOnCalls []gocec.LogicalAddress
	StandbyCalls []gocec.LogicalAddress

	// ActiveSourceAddress is returned by ActiveSource, which is counted in
	// ActiveSourceRequests.
	ActiveSourceAddress  gocec.LogicalAddress
	ActiveSourceRequests int

	// ScanHook is called by Scan when set, which is counted in ScanCount.
	ScanHook  func()
	ScanCount int

	// powerStatus is what libcec reports per device, unknown when not set.
	powerStatus map[gocec.LogicalAddress]gocec.PowerStatus
	handlers    map[gocec.Opcode][]func(gocec.Message)
}

// NewCec returns an adapter with the logical address.
func NewCec(address gocec.LogicalAddress) *Cec {
	return &Cec{Address: address}
}

func (cec *Cec) Transmit(message gocec.Message) {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	cec.Transmitted = append(cec.Transmitted, message)
}

func (cec *Cec) AdapterAddress() gocec.LogicalAddress {
	return cec.Address
}

func (cec *Cec) AdapterPath() string {
	return "/dev/cec0"
}

func (cec *Cec) PowerOn(address gocec.LogicalAddress) {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	cec.PowerOnCalls = append(cec.PowerOnCalls, address)
}

func (cec *Cec) Standby(address gocec.LogicalAddress) {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	cec.StandbyCalls = append(cec.StandbyCalls, address)
}

func (cec *Cec) PowerStatus(address gocec.LogicalAddress) gocec.PowerStatus {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	if status, ok := cec.powerStatus[address]; ok {
		return status
	}
	return gocec.PowerStatusUnknown
}

// SetPowerStatus sets what PowerStatus returns for the device.
func (cec *Cec) SetPowerStatus(address gocec.LogicalAddress, status gocec.PowerStatus) {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	if cec.powerStatus == nil {
		cec.powerStatus = make(map[gocec.LogicalAddress]gocec.PowerStatus)
	}
	cec.powerStatus[address] = status
}

func (cec *Cec) ActiveSource() gocec.LogicalAddress {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	cec.ActiveSourceRequests++
	return cec.ActiveSourceAddress
}

func (cec *Cec) Scan() {
	cec.Mux.Lock()
	cec.ScanCount++
	scan := cec.ScanHook
	cec.Mux.Unlock()

	if scan != nil {
		scan()
	}
}

func (cec *Cec) RegisterMessageHandler(handler func(gocec.Message), opcodes ...gocec.Opcode) {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()
	if cec.handlers == nil {
		cec.handlers = make(map[gocec.Opcode][]func(gocec.Message))
	}
	for _, opcode := range opcodes {
		cec.handlers[opcode] = append(cec.handlers[opcode], handler)
	}
}

// Receive simulates a message received from a device.
func (cec *Cec) Receive(message gocec.Message) {
	cec.Mux.Lock()
	handlers := append(([]func(gocec.Message))(nil), cec.handlers[message.Opcode()]...)
	cec.Mux.Unlock()

	for _, handler := range handlers {
		handler(message)
	}
}

// Sent returns the transmitted messages, optionally only those with one of the opcodes.
func (cec *Cec) Sent(opcodes ...gocec.Opcode) []gocec.Message {
	cec.Mux.Lock()
	defer cec.Mux.Unlock()

	var messages []gocec.Message
	for _, message := range cec.Transmitted {
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

// Message is a published MQTT message.
type Message struct {
	Topic    string
	Retained bool
	Payload  string
}

// Mqtt is an MQTT client which records what's published, and delivers
// messages to the subscriptions with Deliver. Topics are built as
// cec2mqtt/<mqtt topic of the device>/<suffix>. Lock Mux to read the fields
// while the code under test runs.
type Mqtt struct {
	Mux               sync.Mutex
	Published         []Message
	Subscriptions     map[string]func([]byte)
	ConnectedHandlers []func()
}

func NewMqtt() *Mqtt {
	return &Mqtt{Subscriptions: make(map[string]func([]byte))}
}

func (m *Mqtt) BuildTopic(d *device.Device, suffix string) string {
	return "cec2mqtt/" + d.Config.MqttTopic + "/" + suffix
}

func (m *Mqtt) Publish(topic string, qos byte, retained bool, payload interface{}) {
	m.Mux.Lock()
	defer m.Mux.Unlock()

	var text string
	switch p := payload.(type) {
	case string:
		text = p
	case []byte:
		text = string(p)
	}
	m.Published = append(m.Published, Message{Topic: topic, Retained: retained, Payload: text})
}

func (m *Mqtt) Subscribe(topic string, qos byte, callback func([]byte)) {
	m.Mux.Lock()
	defer m.Mux.Unlock()
	m.Subscriptions[topic] = callback
}

func (m *Mqtt) RegisterConnectedHandler(handler func()) {
	m.Mux.Lock()
	defer m.Mux.Unlock()
	m.ConnectedHandlers = append(m.ConnectedHandlers, handler)
}

// Subscribed returns whether the topic is subscribed.
func (m *Mqtt) Subscribed(topic string) bool {
	m.Mux.Lock()
	defer m.Mux.Unlock()
	_, ok := m.Subscriptions[topic]
	return ok
}

// Deliver simulates an incoming MQTT message.
func (m *Mqtt) Deliver(t *testing.T, topic string, payload string) {
	t.Helper()
	m.Mux.Lock()
	callback, ok := m.Subscriptions[topic]
	m.Mux.Unlock()
	if !ok {
		t.Fatalf("no subscription for topic %q", topic)
	}
	callback([]byte(payload))
}

// Reconnect calls the connected handlers, like after reconnecting to the broker.
func (m *Mqtt) Reconnect() {
	m.Mux.Lock()
	handlers := append(([]func())(nil), m.ConnectedHandlers...)
	m.Mux.Unlock()

	for _, handler := range handlers {
		handler()
	}
}

// Messages returns the payloads published on the topic.
func (m *Mqtt) Messages(topic string) []string {
	m.Mux.Lock()
	defer m.Mux.Unlock()

	var payloads []string
	for _, p := range m.Published {
		if p.Topic == topic {
			payloads = append(payloads, p.Payload)
		}
	}
	return payloads
}

// Reset forgets the published messages.
func (m *Mqtt) Reset() {
	m.Mux.Lock()
	defer m.Mux.Unlock()
	m.Published = nil
}

// Discovery returns the latest Home Assistant device discovery message of the
// device and its components, or nil when none was published.
func (m *Mqtt) Discovery(t *testing.T, deviceId string) (map[string]any, map[string]map[string]any) {
	t.Helper()
	messages := m.Messages("homeassistant/device/" + deviceId + "/config")
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
