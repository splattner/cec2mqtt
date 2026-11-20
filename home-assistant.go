package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

func init() {
	RegisterInitializer(100, InitHomeAssistantBridge)
}

type HomeAssistantBirthHandler func()

// removedComponents lists components published by earlier versions which no
// longer exist. Their retained discovery messages are cleared, so Home
// Assistant removes them. Entries are {platform, object id}.
var removedComponents = [][2]string{
	{"button", "select_source"}, // replaced by the "source" select on the TV
}

const migrateDiscoveryPayload = `{"migrate_discovery":true}`

// HomeAssistantBridge publishes Home Assistant MQTT discovery messages. It uses
// device based discovery: one retained message per CEC device, on
// <prefix>/device/<device id>/config, containing all its components.
//
// Earlier versions published one message per component, on
// <prefix>/<platform>/<device id>/<object id>/config. When such a message is
// still retained it is migrated as described in the Home Assistant docs, so
// entities keep their entity ID, name and history.
type HomeAssistantBridge struct {
	discoveryPrefix string
	mqtt            mqttClient
	config          *Config

	// publishDelay batches the components of a device which are registered
	// shortly after each other into a single discovery message.
	publishDelay time.Duration

	mux           sync.Mutex
	closed        bool
	birthHandlers []HomeAssistantBirthHandler
	devices       map[string]*haDevice
	// legacyTopics holds the single component discovery topics which are
	// being watched for retained messages of earlier versions.
	legacyTopics map[string]bool
}

type haDevice struct {
	device     *Device
	components map[string]map[string]interface{}
	timer      *time.Timer
}

func newHomeAssistantBridge(config *Config, mqtt mqttClient) *HomeAssistantBridge {
	return &HomeAssistantBridge{
		discoveryPrefix: config.HomeAssistant.DiscoveryPrefix,
		mqtt:            mqtt,
		config:          config,
		publishDelay:    500 * time.Millisecond,
		devices:         make(map[string]*haDevice),
		legacyTopics:    make(map[string]bool),
	}
}

func InitHomeAssistantBridge(container *Container) {
	config := container.Get("config").(*Config)
	if !config.HomeAssistant.Enable {
		slog.Info("Home assistant integration is not enabled, skipping")
		return
	}

	mqtt := container.Get("mqtt").(*Mqtt)
	bridge := newHomeAssistantBridge(config, mqtt)

	mqtt.Subscribe(config.HomeAssistant.BirthTopic, 0, func(payload []byte) {
		slog.Debug("Received message on Home Assistant birth topic", "payload", string(payload))

		if string(payload) == config.HomeAssistant.BirthPayload {
			slog.Info("Received Home Assistant birth message")
			bridge.mux.Lock()
			handlers := append([]HomeAssistantBirthHandler(nil), bridge.birthHandlers...)
			bridge.mux.Unlock()

			for _, handler := range handlers {
				handler()
			}
		}
	})

	container.Register("home-assistant", bridge)
}

func (bridge *HomeAssistantBridge) RegisterBirthHandler(handler HomeAssistantBirthHandler) {
	bridge.mux.Lock()
	defer bridge.mux.Unlock()
	bridge.birthHandlers = append(bridge.birthHandlers, handler)
}

func (bridge *HomeAssistantBridge) RegisterSwitch(device *Device, property string) {
	config := bridge.createConfig(device, property)
	config["command_topic"] = bridge.mqtt.BuildTopic(device, property+"/set")
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	bridge.register("switch", device, property, config)
}

func (bridge *HomeAssistantBridge) RegisterBinarySensor(device *Device, property string) {
	config := bridge.createConfig(device, property)
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	bridge.register("binary_sensor", device, property, config)
}

// RegisterSensor registers a numeric or text sensor reading the property's state topic.
func (bridge *HomeAssistantBridge) RegisterSensor(device *Device, property string, unit string) {
	config := bridge.createConfig(device, property)
	if unit != "" {
		config["unit_of_measurement"] = unit
	}

	bridge.register("sensor", device, property, config)
}

// RegisterButton registers a button which publishes payload to the command topic
// of commandProperty when pressed. objectId identifies the button itself.
func (bridge *HomeAssistantBridge) RegisterButton(device *Device, objectId string, commandProperty string, payload string) {
	config := bridge.createConfig(device, objectId)
	delete(config, "state_topic")
	config["command_topic"] = bridge.mqtt.BuildTopic(device, commandProperty+"/set")
	config["payload_press"] = payload

	bridge.register("button", device, objectId, config)
}

// RegisterSelect registers a select entity. The state topic contains one of the
// options (or "None" for unknown), the command topic receives the selected option.
func (bridge *HomeAssistantBridge) RegisterSelect(device *Device, property string, options []string) {
	config := bridge.createConfig(device, property)
	config["command_topic"] = bridge.mqtt.BuildTopic(device, property+"/set")
	config["options"] = options

	bridge.register("select", device, property, config)
}

// RegisterEvent registers an event entity. The state topic receives JSON
// messages like {"event_type": "<one of eventTypes>"}.
func (bridge *HomeAssistantBridge) RegisterEvent(device *Device, property string, eventTypes []string) {
	config := bridge.createConfig(device, property)
	config["event_types"] = eventTypes

	bridge.register("event", device, property, config)
}

// register adds or updates a component of the device and schedules publishing
// the device's discovery message.
func (bridge *HomeAssistantBridge) register(platform string, device *Device, objectId string, config map[string]interface{}) {
	config["platform"] = platform

	bridge.mux.Lock()
	entry, ok := bridge.devices[device.Id]
	if !ok {
		entry = &haDevice{components: make(map[string]map[string]interface{})}
		bridge.devices[device.Id] = entry
	}
	entry.device = device
	entry.components[objectId] = config
	bridge.mux.Unlock()

	if !ok {
		for _, removed := range removedComponents {
			bridge.watchLegacyTopic(device, removed[0], removed[1], false)
		}
	}
	bridge.watchLegacyTopic(device, platform, objectId, true)

	bridge.schedulePublish(device.Id)
}

// watchLegacyTopic subscribes to the single component discovery topic which an
// earlier version used for the component. The broker delivers the retained
// message, if there still is one, which is then migrated (or removed).
func (bridge *HomeAssistantBridge) watchLegacyTopic(device *Device, platform string, objectId string, migrate bool) {
	topic := fmt.Sprintf("%s/%s/%s/%s/config", bridge.discoveryPrefix, platform, device.Id, objectId)

	bridge.mux.Lock()
	watched := bridge.legacyTopics[topic]
	bridge.legacyTopics[topic] = true
	bridge.mux.Unlock()

	if watched {
		return
	}

	bridge.mqtt.Subscribe(topic, 0, func(payload []byte) {
		// Empty payloads and our own migration message are the result of a
		// completed (or ongoing) migration.
		if len(payload) == 0 || string(payload) == migrateDiscoveryPayload {
			return
		}

		if !migrate {
			slog.Info("Removing component of an earlier version from Home Assistant", "topic", topic)
			bridge.mqtt.Publish(topic, 0, true, "")
			return
		}

		slog.Info("Migrating Home Assistant component to device discovery", "topic", topic)
		// 1. Unload the component, keeping its settings.
		bridge.mqtt.Publish(topic, 0, true, migrateDiscoveryPayload)
		// 2. Discover it again through the device discovery topic.
		bridge.publish(device.Id)
		// 3. Clean up the single component discovery message.
		bridge.mqtt.Publish(topic, 0, true, "")
	})
}

func (bridge *HomeAssistantBridge) schedulePublish(deviceId string) {
	if bridge.publishDelay <= 0 {
		bridge.publish(deviceId)
		return
	}

	bridge.mux.Lock()
	defer bridge.mux.Unlock()

	if bridge.closed {
		return
	}

	entry := bridge.devices[deviceId]
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = time.AfterFunc(bridge.publishDelay, func() {
		bridge.publish(deviceId)
	})
}

// Close stops publishing discovery messages which are still pending.
func (bridge *HomeAssistantBridge) Close() {
	bridge.mux.Lock()
	defer bridge.mux.Unlock()

	bridge.closed = true
	for _, entry := range bridge.devices {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
}

// publish sends the device discovery message with all registered components.
func (bridge *HomeAssistantBridge) publish(deviceId string) {
	bridge.mux.Lock()
	entry, ok := bridge.devices[deviceId]
	if !ok || bridge.closed {
		bridge.mux.Unlock()
		return
	}

	device := entry.device
	components := make(map[string]interface{}, len(entry.components))
	for objectId, config := range entry.components {
		components[objectId] = config
	}
	bridge.mux.Unlock()

	payload := map[string]interface{}{
		"device": map[string]interface{}{
			"identifiers":  []string{"cec2mqtt_" + device.Id},
			"name":         device.CecDevice.OSD,
			"sw_version":   "Cec2Mqtt " + BuildVersion,
			"manufacturer": device.CecDevice.vendor.String(),
		},
		"origin": map[string]interface{}{
			"name":        "cec2mqtt",
			"sw_version":  BuildVersion,
			"support_url": "https://github.com/splattner/cec2mqtt",
		},
		"components": components,
	}

	if bridge.config.Mqtt.StateTopic != "" {
		payload["availability_topic"] = bridge.config.Mqtt.StateTopic
		payload["payload_available"] = bridge.config.Mqtt.BirthMessage
		payload["payload_not_available"] = bridge.config.Mqtt.WillMessage
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to convert Home Assistant configuration to JSON",
			"device.id", device.Id,
			"error", err,
		)
		return
	}

	slog.Info("Registering device in Home Assistant",
		"device.id", device.Id,
		"components", len(components),
	)
	slog.Debug("Home Assistant discovery message", "device.id", device.Id, "config", string(encoded))

	topic := fmt.Sprintf("%s/device/%s/config", bridge.discoveryPrefix, device.Id)
	bridge.mqtt.Publish(topic, 0, true, encoded)
}

// entityNames holds the Home Assistant entity names per property. Home Assistant
// prefixes these with the device name, so they must not contain it themselves.
var entityNames = map[string]string{
	"power":            "Power",
	"is_active_source": "Active source",
	"source":           "Source",
	"volume":           "Volume",
	"volume_up":        "Volume up",
	"volume_down":      "Volume down",
	"mute":             "Mute",
	"remote_key":       "Remote key",
}

// createConfig creates the component configuration. The unique_id must stay
// the same across versions, otherwise Home Assistant creates new entities.
func (bridge *HomeAssistantBridge) createConfig(device *Device, property string) map[string]interface{} {
	name, ok := entityNames[property]
	if !ok {
		name = property
	}

	return map[string]interface{}{
		"state_topic": bridge.mqtt.BuildTopic(device, property),
		"name":        name,
		"unique_id":   device.Id + "_" + property + "_" + bridge.config.Mqtt.BaseTopic,
	}
}
