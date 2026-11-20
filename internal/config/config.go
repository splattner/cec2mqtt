// Package config reads and validates config.yaml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Mqtt struct {
	Host         string `yaml:"host"`
	Username     string
	Password     string
	StateTopic   string `yaml:"state_topic"`
	BirthMessage string `yaml:"birth_message"`
	WillMessage  string `yaml:"will_message"`
	BaseTopic    string `yaml:"base_topic"`
	ClientId     string `yaml:"client_id"`
}

type Cec struct {
	// Adapter is the path of the CEC adapter to use, e.g. /dev/cec0 or
	// /dev/ttyACM0. When empty the first adapter found is used.
	Adapter string `yaml:"adapter"`
}

type HomeAssistant struct {
	Enable          bool   `yaml:"enable"`
	DiscoveryPrefix string `yaml:"discovery_prefix"`
	BirthTopic      string `yaml:"birth_topic"`
	BirthPayload    string `yaml:"birth_payload"`
}

type Config struct {
	Mqtt          Mqtt
	Cec           Cec           `yaml:"cec"`
	HomeAssistant HomeAssistant `yaml:"home_assistant"`
}

func Load(configPath string) (*Config, error) {
	logContext := slog.With("config_file", configPath+"config.yaml")
	logContext.Info("Reading configuration")
	data, err := os.ReadFile(configPath + "config.yaml")
	if nil != err {
		logContext.Error("Configuration could not be read", "error", err)
		return nil, err
	}

	config, err := parse(data, logContext)
	if err != nil {
		logContext.Error("Configuration is invalid", "error", err)
		return nil, err
	}

	return config, nil
}

// parseConfig parses, completes and validates the configuration.
func parse(data []byte, logContext *slog.Logger) (*Config, error) {
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}

	// Unknown options are most likely typos (like base-topic instead of
	// base_topic). They're reported, but don't stop cec2mqtt from starting,
	// so options removed in the future don't break existing configurations.
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	var typeErr *yaml.TypeError
	if err := strict.Decode(&Config{}); errors.As(err, &typeErr) {
		for _, problem := range typeErr.Errors {
			logContext.Warn("Ignoring unknown configuration option", "problem", problem)
		}
	}

	config.applyDefaults()

	for _, warning := range config.warnings() {
		logContext.Warn(warning)
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	return &config, nil
}

func (config *Config) applyDefaults() {
	config.Mqtt.BaseTopic = strings.Trim(config.Mqtt.BaseTopic, "/")

	// Without birth and will message the state topic is never written, and
	// Home Assistant can't tell whether cec2mqtt is available.
	if config.Mqtt.StateTopic != "" {
		if config.Mqtt.BirthMessage == "" {
			config.Mqtt.BirthMessage = "online"
		}
		if config.Mqtt.WillMessage == "" {
			config.Mqtt.WillMessage = "offline"
		}
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
}

// warnings returns problems which don't stop cec2mqtt from working.
func (config *Config) warnings() []string {
	var warnings []string

	if config.Mqtt.StateTopic == "" && (config.Mqtt.BirthMessage != "" || config.Mqtt.WillMessage != "") {
		warnings = append(warnings, "mqtt.birth_message and mqtt.will_message are ignored without mqtt.state_topic")
	}

	if config.HomeAssistant.Enable && config.Mqtt.StateTopic == "" {
		warnings = append(warnings, "mqtt.state_topic is not set, Home Assistant won't show the entities as unavailable while cec2mqtt is stopped")
	}

	return warnings
}

// validate returns all problems which stop cec2mqtt from working.
func (config *Config) validate() error {
	var errs []error

	if config.Mqtt.Host == "" {
		errs = append(errs, errors.New("mqtt.host is required, e.g. tcp://192.168.1.10:1883"))
	} else if err := validateBroker(config.Mqtt.Host); err != nil {
		errs = append(errs, fmt.Errorf("mqtt.host: %w", err))
	}

	if config.Mqtt.BaseTopic == "" {
		errs = append(errs, errors.New("mqtt.base_topic is required, e.g. cec2mqtt"))
	}

	topics := []struct{ option, topic string }{
		{"mqtt.base_topic", config.Mqtt.BaseTopic},
		{"mqtt.state_topic", config.Mqtt.StateTopic},
	}
	if config.HomeAssistant.Enable {
		topics = append(topics,
			struct{ option, topic string }{"home_assistant.discovery_prefix", config.HomeAssistant.DiscoveryPrefix},
			struct{ option, topic string }{"home_assistant.birth_topic", config.HomeAssistant.BirthTopic},
		)
	}
	for _, t := range topics {
		if strings.ContainsAny(t.topic, "+#\x00") {
			errs = append(errs, fmt.Errorf("%s: %q must not contain the MQTT wildcards + or #", t.option, t.topic))
		}
	}

	return errors.Join(errs...)
}

// brokerSchemes are the URL schemes supported by the MQTT client, and whether
// they need a port.
var brokerSchemes = map[string]bool{
	"tcp": true, "mqtt": true,
	"ssl": true, "tls": true, "mqtts": true, "mqtt+ssl": true, "tcps": true,
	"ws": false, "wss": false,
	"unix": false,
}

// validateBroker checks the broker address the way the MQTT client interprets
// it: without a scheme tcp:// is assumed, and a leading colon means localhost.
func validateBroker(host string) error {
	address := host
	if strings.HasPrefix(address, ":") {
		address = "127.0.0.1" + address
	}
	if !strings.Contains(address, "://") {
		address = "tcp://" + address
	}

	uri, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("%q is not a valid broker address: %w", host, err)
	}

	needsPort, ok := brokerSchemes[uri.Scheme]
	if !ok {
		return fmt.Errorf("%q has the unsupported scheme %q, use tcp://, ssl://, ws://, wss:// or unix://", host, uri.Scheme)
	}

	if uri.Scheme == "unix" {
		if uri.Path == "" && uri.Host == "" {
			return fmt.Errorf("%q has no socket path", host)
		}
		return nil
	}

	if uri.Hostname() == "" {
		return fmt.Errorf("%q has no host name", host)
	}
	if needsPort && uri.Port() == "" {
		return fmt.Errorf("%q has no port, e.g. %s://%s:1883", host, uri.Scheme, uri.Hostname())
	}

	return nil
}
