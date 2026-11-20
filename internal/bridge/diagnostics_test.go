package bridge

import (
	"slices"
	"testing"

	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestDiagnosticsBridge(t *testing.T) {
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newDiagnostics(mqtt)
	bridge.haBridge = newTestDiscovery(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	player := fake.AddDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x12, 0x00}, "Player")

	want := map[string]string{
		"cec2mqtt/Player/physical_address": "1.2.0.0",
		"cec2mqtt/Player/logical_address":  "Playback device 1 (4)",
		"cec2mqtt/Player/vendor":           "Samsung",
	}
	for topic, value := range want {
		if got := mqtt.Messages(topic); !slices.Equal(got, []string{value}) {
			t.Errorf("%s: got %q, want [%q]", topic, got, value)
		}
	}

	_, components := mqtt.Discovery(t, player.Id)
	for _, diagnostic := range diagnosticProperties {
		component := components[diagnostic.property]
		if component["platform"] != "sensor" || component["entity_category"] != "diagnostic" || component["icon"] != diagnostic.icon {
			t.Errorf("unexpected component %s: %v", diagnostic.property, component)
		}
		if component["state_topic"] != "cec2mqtt/Player/"+diagnostic.property {
			t.Errorf("%s: state topic %v", diagnostic.property, component["state_topic"])
		}
	}
	if components["physical_address"]["name"] != "Physical address" {
		t.Errorf("unexpected name: %v", components["physical_address"]["name"])
	}

	// Resent after a reconnect or Home Assistant restart.
	mqtt.Reset()
	bridge.resendAll()
	for topic, value := range want {
		if got := mqtt.Messages(topic); !slices.Equal(got, []string{value}) {
			t.Errorf("after resend %s: got %q, want [%q]", topic, got, value)
		}
	}
	if payload, _ := mqtt.Discovery(t, player.Id); payload == nil {
		t.Error("discovery not resent")
	}
}

func TestDiagnosticsWithoutHomeAssistant(t *testing.T) {
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newDiagnostics(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")

	if got := mqtt.Messages("cec2mqtt/TV/logical_address"); !slices.Equal(got, []string{"TV (0)"}) {
		t.Errorf("got %q, want [TV (0)]", got)
	}
}

func TestLogicalAddressName(t *testing.T) {
	tests := map[gocec.LogicalAddress]string{
		gocec.DeviceTV:              "TV (0)",
		gocec.DeviceAudiosystem:     "Audio system (5)",
		gocec.DevicePlaybackDevice3: "Playback device 3 (11)",
		gocec.DeviceUnknown:         "Unknown (255)",
	}
	for address, want := range tests {
		if got := logicalAddressName(address); got != want {
			t.Errorf("logicalAddressName(%d) = %q, want %q", address, got, want)
		}
	}
}
