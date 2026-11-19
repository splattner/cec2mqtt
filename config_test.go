package main

import (
	"os"
	"path/filepath"
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
