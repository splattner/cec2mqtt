package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

func init() {
	RegisterInitializer(100, InitHomeAssistantBridge)
}

type HomeAssistantBirthHandler func()

type HomeAssistantBridge struct {
	discoveryPrefix string
	mqtt            *Mqtt
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
	topic := strings.Builder{}
	fmt.Fprintf(&topic, "%s/switch/%s/%s/config", bridge.discoveryPrefix, device.Id, property)

	config := bridge.createConfig(device, property)
	config["command_topic"] = bridge.mqtt.BuildTopic(device, property+"/set")
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	encoded, err := json.Marshal(config)
	if err != nil {
		slog.Error("Failed to convert switch configuration to JSON",
			"device.id", device.Config.Id,
			"property", property,
			"config", config,
			"error", err,
		)

		return
	}

	slog.Info("Registering switch in Home Assistant",
		"device.id", device.Config.Id,
		"property", property,
		"config", string(encoded),
	)

	bridge.mqtt.Publish(topic.String(), 0, true, encoded)
}

func (bridge *HomeAssistantBridge) RegisterBinarySensor(device *Device, property string) {
	topic := strings.Builder{}
	fmt.Fprintf(&topic, "%s/binary_sensor/%s/%s/config", bridge.discoveryPrefix, device.Id, property)

	config := bridge.createConfig(device, property)
	config["payload_on"] = "on"
	config["payload_off"] = "off"

	encoded, err := json.Marshal(config)
	if err != nil {
		slog.Error("Failed to convert binary sensor configuration to JSON",
			"device.id", device.Config.Id,
			"property", property,
			"config", config,
			"error", err,
		)

		return
	}

	slog.Info("Registering binary sensor in Home Assistant",
		"device.id", device.Config.Id,
		"property", property,
		"config", string(encoded),
	)

	bridge.mqtt.Publish(topic.String(), 0, true, encoded)
}

// entityNames holds the Home Assistant entity names per property. Home Assistant
// prefixes these with the device name, so they must not contain it themselves.
var entityNames = map[string]string{
	"power":            "Power",
	"is_active_source": "Active source",
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
