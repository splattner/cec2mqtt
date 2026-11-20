package bridge

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/version"
)

func newTestBridgeControls(t *testing.T) (*Controls, *fake.Cec, *fake.Mqtt, *int) {
	t.Helper()
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	mqtt := fake.NewMqtt()
	ha := newTestDiscovery(mqtt)
	hookCalls := 0
	controls := setupControls(testAppConfig(true), cec, mqtt, ha, func() { hookCalls++ })
	return controls, cec, mqtt, &hookCalls
}

func TestBridgeControlsDiscovery(t *testing.T) {
	controls, _, mqtt, _ := newTestBridgeControls(t)
	defer controls.Close()

	payload, components := mqtt.Discovery(t, "bridge_cec2mqtt")
	if payload == nil {
		t.Fatal("no discovery message for the bridge device")
	}

	device := payload["device"].(map[string]any)
	if device["name"] != "Cec2Mqtt" || device["manufacturer"] != "Cec2Mqtt" || device["via_device"] != nil {
		t.Errorf("unexpected device info: %v", device)
	}

	buttons := map[string]string{"standby_all": "cec2mqtt/bridge/standby_all/set", "rescan": "cec2mqtt/bridge/rescan/set"}
	for id, topic := range buttons {
		if components[id]["platform"] != "button" || components[id]["command_topic"] != topic || components[id]["payload_press"] != "press" {
			t.Errorf("unexpected button %s: %v", id, components[id])
		}
	}
	for _, id := range []string{"version", "adapter"} {
		if components[id]["platform"] != "sensor" || components[id]["entity_category"] != "diagnostic" {
			t.Errorf("unexpected diagnostic sensor %s: %v", id, components[id])
		}
	}

	if got := mqtt.Messages("cec2mqtt/bridge/version"); !slices.Equal(got, []string{version.Version}) {
		t.Errorf("version: got %q", got)
	}
	if got := mqtt.Messages("cec2mqtt/bridge/adapter"); !slices.Equal(got, []string{"/dev/cec0"}) {
		t.Errorf("adapter: got %q", got)
	}

	// Republished after a reconnect.
	mqtt.Reset()
	mqtt.Reconnect()
	if got := mqtt.Messages("cec2mqtt/bridge/version"); len(got) != 1 {
		t.Errorf("version not republished: %q", got)
	}
}

func TestBridgeControlsViaDevice(t *testing.T) {
	mqtt := fake.NewMqtt()
	ha := newTestDiscovery(mqtt)
	controls := setupControls(testAppConfig(true), fake.NewCec(0), mqtt, ha, nil)
	defer controls.Close()

	registry := fake.NewRegistry(t)
	tv := fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	ha.RegisterSwitch(tv, "power")

	payload, _ := mqtt.Discovery(t, tv.Id)
	if via := payload["device"].(map[string]any)["via_device"]; via != "cec2mqtt_bridge_cec2mqtt" {
		t.Errorf("via_device = %v, want cec2mqtt_bridge_cec2mqtt", via)
	}
}

func TestBridgeControlsStandbyAll(t *testing.T) {
	controls, cec, mqtt, hookCalls := newTestBridgeControls(t)
	defer controls.Close()

	mqtt.Deliver(t, "cec2mqtt/bridge/standby_all/set", "press")

	// From the adapter (1) to broadcast (F).
	assertMessages(t, cec.Sent(), []gocec.Message{{0x1F, byte(gocec.OpcodeStandby)}})
	if *hookCalls != 1 {
		t.Errorf("standby hook called %d times, want 1", *hookCalls)
	}
}

func TestBridgeControlsRescan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controls, cec, mqtt, _ := newTestBridgeControls(t)

		release := make(chan struct{})
		cec.ScanHook = func() { <-release }

		// Presses while a scan is running are ignored.
		mqtt.Deliver(t, "cec2mqtt/bridge/rescan/set", "press")
		mqtt.Deliver(t, "cec2mqtt/bridge/rescan/set", "press")
		synctest.Wait()
		if cec.ScanCount != 1 {
			t.Errorf("scans = %d, want 1 while the first is running", cec.ScanCount)
		}

		close(release)
		synctest.Wait()

		mqtt.Deliver(t, "cec2mqtt/bridge/rescan/set", "press")
		controls.Close() // waits for the scan
		if cec.ScanCount != 2 {
			t.Errorf("scans = %d, want 2", cec.ScanCount)
		}
	})
}

func TestBridgeDeviceId(t *testing.T) {
	config := testAppConfig(true)
	config.Mqtt.BaseTopic = "home/living room/cec"
	if got := newBridgeDevice(config).Id; got != "bridge_home_living_room_cec" {
		t.Errorf("Id = %q", got)
	}
}
