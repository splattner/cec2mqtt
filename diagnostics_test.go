package main

import (
	"slices"
	"sync"
	"testing"

	"github.com/RobertMe/gocec"
)

func TestDiagnosticsBridge(t *testing.T) {
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newDiagnosticsBridge(mqtt)
	bridge.haBridge = newTestHomeAssistantBridge(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	player := addTestDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x12, 0x00}, "Player")

	want := map[string]string{
		"cec2mqtt/Player/physical_address": "1.2.0.0",
		"cec2mqtt/Player/logical_address":  "Playback device 1 (4)",
		"cec2mqtt/Player/vendor":           "Samsung",
	}
	for topic, value := range want {
		if got := mqtt.messages(topic); !slices.Equal(got, []string{value}) {
			t.Errorf("%s: got %q, want [%q]", topic, got, value)
		}
	}

	_, components := mqtt.discovery(t, player.Id)
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
	mqtt.reset()
	bridge.resendAll()
	for topic, value := range want {
		if got := mqtt.messages(topic); !slices.Equal(got, []string{value}) {
			t.Errorf("after resend %s: got %q, want [%q]", topic, got, value)
		}
	}
	if payload, _ := mqtt.discovery(t, player.Id); payload == nil {
		t.Error("discovery not resent")
	}
}

func TestDiagnosticsWithoutHomeAssistant(t *testing.T) {
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newDiagnosticsBridge(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")

	if got := mqtt.messages("cec2mqtt/TV/logical_address"); !slices.Equal(got, []string{"TV (0)"}) {
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

// TestVendorNameConcurrent fails with garbled names if vendorName isn't
// guarded, as gocec converts every vendor in the same C buffer. (The race
// detector doesn't see that, the buffer is C memory.)
func TestVendorNameConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for _, vendor := range []gocec.Vendor{gocec.VendorSamsung, gocec.VendorLg, gocec.VendorSony, gocec.VendorPhilips} {
		want := vendorName(vendor)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if got := vendorName(vendor); got != want {
					t.Errorf("vendorName(%v) = %q, want %q", uint(vendor), got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}
