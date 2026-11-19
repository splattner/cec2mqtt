package main

import "github.com/RobertMe/gocec"

// cecSender is the part of *Cec used to send messages. It's an interface so
// bridges can be tested without a CEC adapter.
type cecSender interface {
	Transmit(message gocec.Message)
	AdapterAddress() gocec.LogicalAddress
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
	_ cecSender  = (*Cec)(nil)
	_ mqttClient = (*Mqtt)(nil)
)
