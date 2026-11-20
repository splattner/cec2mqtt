package bridge

import (
	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

// cecSender is the part of the CEC connection (cec.Connection) used by the bridges. It's an interface so
// bridges can be tested without a CEC adapter.
type cecSender interface {
	Transmit(message gocec.Message)
	AdapterAddress() gocec.LogicalAddress

	PowerOn(address gocec.LogicalAddress)
	Standby(address gocec.LogicalAddress)
	// PowerStatus returns libcec's power status of the device, which may be
	// cached for up to 30 seconds.
	PowerStatus(address gocec.LogicalAddress) gocec.PowerStatus
	// ActiveSource asks libcec for the current active source.
	ActiveSource() gocec.LogicalAddress
}

// CEC is a cecSender which also delivers the messages received from devices.
type CEC interface {
	cecSender
	RegisterMessageHandler(handler func(gocec.Message), opcodes ...gocec.Opcode)
	// Scan adds the devices on the bus which aren't known yet.
	Scan()
	AdapterPath() string
}

// MQTT is the part of the MQTT client (mqtt.Client) used by the bridges. It's an interface so
// bridges can be tested without an MQTT broker.
type MQTT interface {
	BuildTopic(device *device.Device, suffix string) string
	Publish(topic string, qos byte, retained bool, payload interface{})
	Subscribe(topic string, qos byte, callback func([]byte))
	RegisterConnectedHandler(handler func())
}
