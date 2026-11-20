package main

import "github.com/RobertMe/gocec"

// cecSender is the part of *Cec used by the bridges. It's an interface so
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

// cecBus is a cecSender which also delivers the messages received from devices.
type cecBus interface {
	cecSender
	RegisterMessageHandler(handler MessageReceivedHandler, opcodes ...gocec.Opcode)
	// Scan adds the devices on the bus which aren't known yet.
	Scan()
	AdapterPath() string
}

// mqttClient is the part of *Mqtt used by the bridges. It's an interface so
// bridges can be tested without an MQTT broker.
type mqttClient interface {
	BuildTopic(device *Device, suffix string) string
	Publish(topic string, qos byte, retained bool, payload interface{})
	Subscribe(topic string, qos byte, callback MessageHandler)
	RegisterConnectedHandler(handler MqttConnectedHandler)
}

var (
	_ cecBus     = (*Cec)(nil)
	_ mqttClient = (*Mqtt)(nil)
)
