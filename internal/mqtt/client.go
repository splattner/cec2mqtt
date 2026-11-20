// Package mqtt wraps the MQTT client: it keeps subscriptions across
// reconnects, and publishes the birth and will messages on the state topic.
package mqtt

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/splattner/cec2mqtt/internal/config"
	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/logging"
)

type ConnectedHandler = func()

type MessageHandler = func(payload []byte)

type subscription struct {
	qos      byte
	callback paho.MessageHandler
}

type Client struct {
	client paho.Client
	config *config.Mqtt

	mux               sync.Mutex
	connectedHandlers []ConnectedHandler
	subscriptions     map[string]subscription
}

func Connect(config *config.Config) (*Client, error) {
	mqttConfig := config.Mqtt

	inst := &Client{
		config:        &mqttConfig,
		subscriptions: make(map[string]subscription),
	}

	options := paho.NewClientOptions()

	options.AddBroker(mqttConfig.Host)

	if mqttConfig.ClientId != "" {
		options.SetClientID(mqttConfig.ClientId)
	}

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

	options.SetConnectionLostHandler(func(_ paho.Client, err error) {
		slog.Warn("Lost connection to MQTT", "error", err)
	})

	options.SetOnConnectHandler(inst.onConnect)

	inst.client = paho.NewClient(options)

	slog.Info("Connecting to MQTT", "host", mqttConfig.Host)

	token := inst.client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, err
	}

	return inst, nil
}

func (m *Client) onConnect(client paho.Client) {
	slog.Info("Connected to MQTT")
	if m.config.StateTopic != "" && m.config.BirthMessage != "" {
		client.Publish(m.config.StateTopic, 0, true, m.config.BirthMessage)
	}

	m.mux.Lock()
	// The session is clean, so subscriptions have to be restored after every (re)connect.
	subscriptions := make(map[string]subscription, len(m.subscriptions))
	for topic, sub := range m.subscriptions {
		subscriptions[topic] = sub
	}
	handlers := append([]ConnectedHandler(nil), m.connectedHandlers...)
	m.mux.Unlock()

	for topic, sub := range subscriptions {
		client.Subscribe(topic, sub.qos, sub.callback)
	}

	for _, handler := range handlers {
		handler()
	}
}

// Close publishes the will message on the state topic, as the broker doesn't
// send it on a clean disconnect, and disconnects from the broker.
func (m *Client) Close() {
	if m.config.StateTopic != "" && m.config.WillMessage != "" && m.client.IsConnected() {
		token := m.client.Publish(m.config.StateTopic, 0, true, m.config.WillMessage)
		if !token.WaitTimeout(2 * time.Second) {
			slog.Warn("Timed out publishing the will message")
		} else if err := token.Error(); err != nil {
			slog.Warn("Failed to publish the will message", "error", err)
		}
	}

	// Gives in-flight messages up to 250ms to be sent.
	m.client.Disconnect(250)
	slog.Info("Disconnected from MQTT")
}

func (m *Client) BuildTopic(device *device.Device, suffix string) string {
	return fmt.Sprintf("%s/%s/%s", m.config.BaseTopic, device.Config.MqttTopic, suffix)
}

func (m *Client) Publish(topic string, qos byte, retained bool, payload interface{}) {
	m.client.Publish(topic, qos, retained, payload)
	logging.Trace("Published MQTT message",
		"topic", topic,
		"qos", qos,
		"retained", retained,
		"payload", payload,
	)
}

func (m *Client) Subscribe(topic string, qos byte, callback MessageHandler) {
	handler := func(_ paho.Client, message paho.Message) {
		logging.Trace("Handling incoming MQTT message",
			"topic", message.Topic(),
			"payload", string(message.Payload()),
		)
		callback(message.Payload())
	}

	m.mux.Lock()
	m.subscriptions[topic] = subscription{qos: qos, callback: handler}
	m.mux.Unlock()

	m.client.Subscribe(topic, qos, handler)
	logging.Trace("Subscribed to MQTT topic",
		"topic", topic,
		"qos", qos,
	)
}

func (m *Client) RegisterConnectedHandler(handler ConnectedHandler) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.connectedHandlers = append(m.connectedHandlers, handler)
}
