package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

type MqttConnectedHandler func()

type MessageHandler func(payload []byte)

type subscription struct {
	qos      byte
	callback mqtt.MessageHandler
}

type Mqtt struct {
	client mqtt.Client
	config *MqttConfig

	mux               sync.Mutex
	connectedHandlers []MqttConnectedHandler
	subscriptions     map[string]subscription
}

func ConnectMqtt(config *Config) (*Mqtt, error) {
	mqttConfig := config.Mqtt

	inst := &Mqtt{
		config:        &mqttConfig,
		subscriptions: make(map[string]subscription),
	}

	options := mqtt.NewClientOptions()

	options.AddBroker(mqttConfig.Host)

	if mqttConfig.Username != "" {
		options.SetUsername(mqttConfig.Username)
	}

	if mqttConfig.Password != "" {
		options.SetPassword(mqttConfig.Password)
	}

	if mqttConfig.StateTopic != "" {
		if mqttConfig.WillMessage != "" {
			options.SetWill(mqttConfig.StateTopic, mqttConfig.WillMessage, 0, true)
		}
	}

	// Keep retrying when the broker isn't reachable (yet), both on startup and after losing the connection.
	options.SetAutoReconnect(true)
	options.SetConnectRetry(true)
	options.SetConnectRetryInterval(5 * time.Second)

	options.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		log.WithFields(log.Fields{
			"error": err,
		}).Warn("Lost connection to MQTT")
	})

	options.SetOnConnectHandler(inst.onConnect)

	inst.client = mqtt.NewClient(options)

	log.WithFields(log.Fields{
		"host": mqttConfig.Host,
	}).Info("Connecting to MQTT")

	token := inst.client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, err
	}

	return inst, nil
}

func (m *Mqtt) onConnect(client mqtt.Client) {
	log.Info("Connected to MQTT")
	if m.config.StateTopic != "" && m.config.BirthMessage != "" {
		client.Publish(m.config.StateTopic, 0, true, m.config.BirthMessage)
	}

	m.mux.Lock()
	// The session is clean, so subscriptions have to be restored after every (re)connect.
	subscriptions := make(map[string]subscription, len(m.subscriptions))
	for topic, sub := range m.subscriptions {
		subscriptions[topic] = sub
	}
	handlers := append([]MqttConnectedHandler(nil), m.connectedHandlers...)
	m.mux.Unlock()

	for topic, sub := range subscriptions {
		client.Subscribe(topic, sub.qos, sub.callback)
	}

	for _, handler := range handlers {
		handler()
	}
}

func (m *Mqtt) BuildTopic(device *Device, suffix string) string {
	return fmt.Sprintf("%s/%s/%s", m.config.BaseTopic, device.Config.MqttTopic, suffix)
}

func (m *Mqtt) Publish(topic string, qos byte, retained bool, payload interface{}) {
	m.client.Publish(topic, qos, retained, payload)
	log.WithFields(log.Fields{
		"topic":    topic,
		"qos":      qos,
		"retained": retained,
		"payload":  payload,
	}).Trace("Published MQTT message")
}

func (m *Mqtt) Subscribe(topic string, qos byte, callback MessageHandler) {
	handler := func(_ mqtt.Client, message mqtt.Message) {
		log.WithFields(log.Fields{
			"topic":   message.Topic(),
			"payload": string(message.Payload()),
		}).Trace("Handling incoming MQTT message")
		callback(message.Payload())
	}

	m.mux.Lock()
	m.subscriptions[topic] = subscription{qos: qos, callback: handler}
	m.mux.Unlock()

	m.client.Subscribe(topic, qos, handler)
	log.WithFields(log.Fields{
		"topic": topic,
		"qos":   qos,
	}).Trace("Subscribed to MQTT topic")
}

func (m *Mqtt) RegisterConnectedHandler(handler MqttConnectedHandler) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.connectedHandlers = append(m.connectedHandlers, handler)
}
