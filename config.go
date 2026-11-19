package main

import (
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type MqttConfig struct {
	Host         string `yaml:"host"`
	Username     string
	Password     string
	StateTopic   string `yaml:"state_topic"`
	BirthMessage string `yaml:"birth_message"`
	WillMessage  string `yaml:"will_message"`
	BaseTopic    string `yaml:"base_topic"`
	ClientId     string `yaml:"client_id"`
}

type CecConfig struct {
	// Adapter is the path of the CEC adapter to use, e.g. /dev/cec0 or
	// /dev/ttyACM0. When empty the first adapter found is used.
	Adapter string `yaml:"adapter"`
}

type HomeAssistantConfig struct {
	Enable          bool   `yaml:"enable"`
	DiscoveryPrefix string `yaml:"discovery_prefix"`
	BirthTopic      string `yaml:"birth_topic"`
	BirthPayload    string `yaml:"birth_payload"`
}

type Config struct {
	Mqtt          MqttConfig
	Cec           CecConfig           `yaml:"cec"`
	HomeAssistant HomeAssistantConfig `yaml:"home_assistant"`
}

func ParseConfig(configPath string) (*Config, error) {
	logContext := slog.With("config_file", configPath+"config.yaml")
	logContext.Info("Reading configuration")
	data, err := os.ReadFile(configPath + "config.yaml")
	if nil != err {
		logContext.Error("Configuration could not be read", "error", err)
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)

	if err != nil {
		logContext.Error("Configuration file could not be parsed as valid YAML", "error", err)
		return nil, err
	}

	if config.HomeAssistant.Enable {
		if config.HomeAssistant.DiscoveryPrefix == "" {
			slog.Debug("Home assistant integration is enabled but discovery prefix is not set. Setting default.")
			config.HomeAssistant.DiscoveryPrefix = "homeassistant"
		} else {
			config.HomeAssistant.DiscoveryPrefix = strings.Trim(config.HomeAssistant.DiscoveryPrefix, "/")
		}

		if config.HomeAssistant.BirthTopic == "" {
			slog.Debug("Home Assistant integration is enabled but birth topic is not set. Setting default.")
			config.HomeAssistant.BirthTopic = config.HomeAssistant.DiscoveryPrefix + "/status"
		}

		if config.HomeAssistant.BirthPayload == "" {
			slog.Debug("Home Assistant integration is enabled but birth payload is not set. Setting default.")
			config.HomeAssistant.BirthPayload = "online"
		}
	}

	return &config, nil
}
