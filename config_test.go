package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir() + "/"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseConfigHomeAssistantDefaults(t *testing.T) {
	dir := writeConfig(t, `
mqtt:
  host: tcp://localhost:1883
  base_topic: cec2mqtt
home_assistant:
  enable: true
`)

	config, err := ParseConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	ha := config.HomeAssistant
	if ha.DiscoveryPrefix != "homeassistant" {
		t.Errorf("DiscoveryPrefix = %q, want %q", ha.DiscoveryPrefix, "homeassistant")
	}
	if ha.BirthTopic != "homeassistant/status" {
		t.Errorf("BirthTopic = %q, want %q", ha.BirthTopic, "homeassistant/status")
	}
	if ha.BirthPayload != "online" {
		t.Errorf("BirthPayload = %q, want %q", ha.BirthPayload, "online")
	}
}

func TestParseConfigHomeAssistantCustom(t *testing.T) {
	dir := writeConfig(t, `
mqtt:
  host: tcp://localhost:1883
  username: user
  password: secret
  base_topic: cec2mqtt
  client_id: livingroom
cec:
  adapter: /dev/cec1
home_assistant:
  enable: true
  discovery_prefix: /custom/
  birth_topic: hass/status
  birth_payload: up
`)

	config, err := ParseConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	if config.Mqtt.Username != "user" || config.Mqtt.Password != "secret" {
		t.Errorf("credentials not parsed: %+v", config.Mqtt)
	}

	if config.Mqtt.ClientId != "livingroom" {
		t.Errorf("ClientId = %q, want %q", config.Mqtt.ClientId, "livingroom")
	}
	if config.Cec.Adapter != "/dev/cec1" {
		t.Errorf("Cec.Adapter = %q, want %q", config.Cec.Adapter, "/dev/cec1")
	}

	ha := config.HomeAssistant
	if ha.DiscoveryPrefix != "custom" {
		t.Errorf("DiscoveryPrefix = %q, want slashes trimmed to %q", ha.DiscoveryPrefix, "custom")
	}
	if ha.BirthTopic != "hass/status" {
		t.Errorf("BirthTopic = %q, want %q", ha.BirthTopic, "hass/status")
	}
	if ha.BirthPayload != "up" {
		t.Errorf("BirthPayload = %q, want %q", ha.BirthPayload, "up")
	}
}

func TestParseConfigHomeAssistantDisabled(t *testing.T) {
	dir := writeConfig(t, `
mqtt:
  host: tcp://localhost:1883
  base_topic: cec2mqtt
`)

	config, err := ParseConfig(dir)
	if err != nil {
		t.Fatal(err)
	}

	if config.HomeAssistant.Enable || config.HomeAssistant.BirthTopic != "" {
		t.Errorf("expected Home Assistant to stay disabled without defaults, got %+v", config.HomeAssistant)
	}
}

func TestParseConfigErrors(t *testing.T) {
	if _, err := ParseConfig(t.TempDir() + "/"); err == nil {
		t.Error("expected error for missing config file")
	}

	if _, err := ParseConfig(writeConfig(t, "mqtt: [not, a, map")); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func parseTestConfig(t *testing.T, content string) (*Config, string, error) {
	t.Helper()
	var logs bytes.Buffer
	config, err := parseConfig([]byte(content), slog.New(slog.NewTextHandler(&logs, nil)))
	return config, logs.String(), err
}

func TestParseConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr []string // substrings of the error, nil when valid
	}{
		{"minimal", "mqtt: {host: 1.2.3.4:1883, base_topic: cec2mqtt}", nil},
		{"localhost shorthand", "mqtt: {host: ':1883', base_topic: cec2mqtt}", nil},
		{"tls", "mqtt: {host: 'ssl://broker:8883', base_topic: cec2mqtt}", nil},
		{"websocket without port", "mqtt: {host: 'wss://broker/mqtt', base_topic: cec2mqtt}", nil},
		{"unix socket", "mqtt: {host: 'unix:///run/mosquitto.sock', base_topic: cec2mqtt}", nil},
		{"empty file", "", []string{"mqtt.host is required", "mqtt.base_topic is required"}},
		{"no port", "mqtt: {host: broker, base_topic: cec2mqtt}", []string{`mqtt.host: "broker" has no port, e.g. tcp://broker:1883`}},
		{"unknown scheme", "mqtt: {host: 'http://broker:1883', base_topic: cec2mqtt}", []string{`unsupported scheme "http"`}},
		{"no host name", "mqtt: {host: 'tcp://:1883', base_topic: cec2mqtt}", []string{"has no host name"}},
		{"invalid address", "mqtt: {host: 'tcp://broker:port', base_topic: cec2mqtt}", []string{"not a valid broker address"}},
		{"only slashes as base topic", "mqtt: {host: 'broker:1883', base_topic: /}", []string{"mqtt.base_topic is required"}},
		{"wildcard in base topic", "mqtt: {host: 'broker:1883', base_topic: cec/+}", []string{"mqtt.base_topic", "wildcards"}},
		{"wildcard in state topic", "mqtt: {host: 'broker:1883', base_topic: cec2mqtt, state_topic: cec2mqtt/#}", []string{"mqtt.state_topic"}},
		{"wildcard in discovery prefix", "mqtt: {host: 'broker:1883', base_topic: cec2mqtt}\nhome_assistant: {enable: true, discovery_prefix: 'ha/#'}", []string{"home_assistant.discovery_prefix", "home_assistant.birth_topic"}},
		{"discovery prefix ignored while disabled", "mqtt: {host: 'broker:1883', base_topic: cec2mqtt}\nhome_assistant: {discovery_prefix: 'ha/#'}", nil},
		{"invalid YAML", "mqtt: [not, a, map", []string{"not valid YAML"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, _, err := parseTestConfig(t, tt.config)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if config == nil {
					t.Fatal("no configuration returned")
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q doesn't contain %q", err, want)
				}
			}
		})
	}
}

func TestParseConfigDefaults(t *testing.T) {
	config, _, err := parseTestConfig(t, `
mqtt:
  host: broker:1883
  base_topic: /cec2mqtt/
  state_topic: cec2mqtt/state
`)
	if err != nil {
		t.Fatal(err)
	}

	if config.Mqtt.BaseTopic != "cec2mqtt" {
		t.Errorf("BaseTopic = %q, want slashes trimmed", config.Mqtt.BaseTopic)
	}
	if config.Mqtt.BirthMessage != "online" || config.Mqtt.WillMessage != "offline" {
		t.Errorf("birth/will = %q/%q, want online/offline", config.Mqtt.BirthMessage, config.Mqtt.WillMessage)
	}

	// Explicit messages are kept.
	config, _, err = parseTestConfig(t, "mqtt: {host: 'broker:1883', base_topic: cec2mqtt, state_topic: s, birth_message: up, will_message: down}")
	if err != nil {
		t.Fatal(err)
	}
	if config.Mqtt.BirthMessage != "up" || config.Mqtt.WillMessage != "down" {
		t.Errorf("birth/will = %q/%q, want up/down", config.Mqtt.BirthMessage, config.Mqtt.WillMessage)
	}
}

func TestParseConfigWarnings(t *testing.T) {
	_, logs, err := parseTestConfig(t, `
mqtt:
  host: broker:1883
  base-topic: typo
  base_topic: cec2mqtt
  will_message: offline
home_assistant:
  enable: true
  discovery-prefix: typo
`)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		`Ignoring unknown configuration option" problem="line 4: field base-topic not found`,
		"field discovery-prefix not found",
		"mqtt.birth_message and mqtt.will_message are ignored without mqtt.state_topic",
		"Home Assistant won't show the entities as unavailable",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("expected warning %q, got logs:\n%s", want, logs)
		}
	}

	// A complete configuration has no warnings.
	_, logs, _ = parseTestConfig(t, "mqtt: {host: 'broker:1883', base_topic: cec2mqtt, state_topic: cec2mqtt/state}\nhome_assistant: {enable: true}")
	if strings.Contains(logs, "WARN") {
		t.Errorf("unexpected warnings:\n%s", logs)
	}
}
