package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

func init() {
	RegisterInitializer(100, InitHomeAssistantBridge)
}

type HomeAssistantBirthHandler func()

type HomeAssistantBridge struct {
	discoveryPrefix string
	mqtt            mqttClient
	config          *Config
	birthHandlers   []HomeAssistantBirthHandler
}

func InitHomeAssistantBridge(container *Container) {
	config := container.Get("config").(*Config)
	if !config.HomeAssistant.Enable {
		slog.Info("Home assistant integration is not enabled, skipping")
		return
	}

	mqtt := container.Get("mqtt").(*Mqtt)
	bridge := &HomeAssistantBridge{
		discoveryPrefix: config.HomeAssistant.DiscoveryPrefix,
		mqtt:            mqtt,
		config:          config,
		birthHandlers:   make([]HomeAssistantBirthHandler, 0),
	}

	mqtt.Subscribe(config.HomeAssistant.BirthTopic, 0, func(payload []byte) {
		slog.Debug("Received message on Home Assistant birth topic", "payload", string(payload))

		if string(payload) == config.HomeAssistant.BirthPayload {
			slog.Info("Received Home Assistant birth message")
			for _, handler := range bridge.birthHandlers {
				handler()
			}
		}
	})

	container.Register("home-assistant", bridge)
}

func (bridge *HomeAssistantBridge) RegisterBirthHandler(handler HomeAssistantBirthHandler) {
	bridge.birthHandlers = append(bridge.birthHandlers, handler)
}

func (bridge *HomeAssistantBridge) RegisterSwitch(device *Device, property string) {
	config := bridge.createConfig(device, property)
	config["command_topic"] = bridge.mqtt.BuildTopic(device, property+"/set")
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	bridge.publishDiscovery("switch", device, property, config)
}

func (bridge *HomeAssistantBridge) RegisterBinarySensor(device *Device, property string) {
	config := bridge.createConfig(device, property)
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	bridge.publishDiscovery("binary_sensor", device, property, config)
}

// RegisterSensor registers a numeric or text sensor reading the property's state topic.
func (bridge *HomeAssistantBridge) RegisterSensor(device *Device, property string, unit string) {
	config := bridge.createConfig(device, property)
	if unit != "" {
		config["unit_of_measurement"] = unit
	}

	bridge.publishDiscovery("sensor", device, property, config)
}

// RegisterButton registers a button which publishes payload to the command topic
// of commandProperty when pressed. objectId identifies the button itself.
func (bridge *HomeAssistantBridge) RegisterButton(device *Device, objectId string, commandProperty string, payload string) {
	config := bridge.createConfig(device, objectId)
	delete(config, "state_topic")
	config["command_topic"] = bridge.mqtt.BuildTopic(device, commandProperty+"/set")
	config["payload_press"] = payload

	bridge.publishDiscovery("button", device, objectId, config)
}

// RegisterEvent registers an event entity. The state topic receives JSON
// messages like {"event_type": "<one of eventTypes>"}.
func (bridge *HomeAssistantBridge) RegisterEvent(device *Device, property string, eventTypes []string) {
	config := bridge.createConfig(device, property)
	config["event_types"] = eventTypes

	bridge.publishDiscovery("event", device, property, config)
}

func (bridge *HomeAssistantBridge) publishDiscovery(component string, device *Device, objectId string, config map[string]interface{}) {
	topic := fmt.Sprintf("%s/%s/%s/%s/config", bridge.discoveryPrefix, component, device.Id, objectId)

	encoded, err := json.Marshal(config)
	if err != nil {
		slog.Error("Failed to convert Home Assistant configuration to JSON",
			"component", component,
			"device.id", device.Config.Id,
			"object_id", objectId,
			"error", err,
		)

		return
	}

	slog.Info("Registering entity in Home Assistant",
		"component", component,
		"device.id", device.Config.Id,
		"object_id", objectId,
		"config", string(encoded),
	)

	bridge.mqtt.Publish(topic, 0, true, encoded)
}

// entityNames holds the Home Assistant entity names per property. Home Assistant
// prefixes these with the device name, so they must not contain it themselves.
var entityNames = map[string]string{
	"power":            "Power",
	"is_active_source": "Active source",
	"select_source":    "Select source",
	"volume":           "Volume",
	"volume_up":        "Volume up",
	"volume_down":      "Volume down",
	"mute":             "Mute",
	"remote_key":       "Remote key",
}

func (bridge *HomeAssistantBridge) createConfig(device *Device, property string) map[string]interface{} {
	name, ok := entityNames[property]
	if !ok {
		name = property
	}

	config := map[string]interface{}{
		"state_topic": bridge.mqtt.BuildTopic(device, property),
		"name":        name,
		"unique_id":   device.Id + "_" + property + "_" + bridge.config.Mqtt.BaseTopic,
		"origin": map[string]interface{}{
			"name":        "cec2mqtt",
			"sw_version":  BuildVersion,
			"support_url": "https://github.com/splattner/cec2mqtt",
		},
	}

	if bridge.config.Mqtt.StateTopic != "" {
		config["availability_topic"] = bridge.config.Mqtt.StateTopic
		config["payload_available"] = bridge.config.Mqtt.BirthMessage
		config["payload_not_available"] = bridge.config.Mqtt.WillMessage
	}

	deviceConfig := map[string]interface{}{
		"identifiers":  []string{"cec2mqtt_" + device.Id},
		"name":         device.CecDevice.OSD,
		"sw_version":   "Cec2Mqtt " + BuildVersion,
		"manufacturer": device.CecDevice.vendor.String(),
	}

	config["device"] = deviceConfig

	return config
}
