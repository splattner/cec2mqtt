package bridge

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestKeyCodes(t *testing.T) {
	for _, name := range KeyNames() {
		code, ok := KeyCode(name)
		if !ok {
			t.Fatalf("KeyCode(%q) not found", name)
		}
		if KeyName(code) != name {
			t.Errorf("KeyName(0x%02X) = %q, want %q (duplicate code?)", code, KeyName(code), name)
		}
	}

	if code, _ := KeyCode("volume_up"); code != 0x41 {
		t.Errorf("volume_up = 0x%02X, want 0x41", code)
	}
	if _, ok := KeyCode("does_not_exist"); ok {
		t.Error("expected unknown key to be rejected")
	}
	if name := KeyName(0x0E); name != "unknown_0x0E" {
		t.Errorf("KeyName(0x0E) = %q", name)
	}
}

func TestSendKey(t *testing.T) {
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	sendKey(cec, gocec.DevicePlaybackDevice1, 0x44)

	want := []gocec.Message{{0x14, 0x44, 0x44}, {0x14, 0x45}}
	assertMessages(t, cec.Sent(), want)
}

func TestSelectSource(t *testing.T) {
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	device := &device.Device{CecDevice: &device.Description{PhysicalAddress: gocec.PhysicalAddress{0x21, 0x00}}}

	selectSource(cec, device)

	want := []gocec.Message{
		{0x10, byte(gocec.OpcodeImageViewOn)},
		{0x1F, byte(gocec.OpcodeSetStreamPath), 0x21, 0x00},
	}
	assertMessages(t, cec.Sent(), want)
}

func assertMessages(t *testing.T, got, want []gocec.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d messages %X, want %d %X", len(got), got, len(want), want)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("message %d = %X, want %X", i, []byte(got[i]), []byte(want[i]))
		}
	}
}

func TestRemoteBridge(t *testing.T) {
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newRemote(cec, mqtt, registry)
	bridge.haBridge = newTestDiscovery(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	tv := fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	player := fake.AddDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")

	// Event entity of the TV is registered right away, others only on the first key press.
	if _, components := mqtt.Discovery(t, tv.Id); components["remote_key"]["platform"] != "event" {
		t.Error("expected event entity for the TV")
	}
	if _, components := mqtt.Discovery(t, player.Id); components != nil {
		t.Error("expected no event entity for the player yet")
	}

	t.Run("send key", func(t *testing.T) {
		mqtt.Deliver(t, "cec2mqtt/Player/key/set", " Play\n")
		assertMessages(t, cec.Sent(), []gocec.Message{{0x14, 0x44, 0x44}, {0x14, 0x45}})

		mqtt.Deliver(t, "cec2mqtt/Player/key/set", "no_such_key")
		if len(cec.Sent()) != 2 {
			t.Error("unknown key must not be sent")
		}
	})

	t.Run("receive key", func(t *testing.T) {
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed), 0x00})

		got := mqtt.Messages("cec2mqtt/Player/remote_key")
		if len(got) != 1 || got[0] != `{"event_type":"select"}` {
			t.Errorf("published %q", got)
		}
		if _, components := mqtt.Discovery(t, player.Id); components["remote_key"]["platform"] != "event" {
			t.Error("expected event entity to be registered on first key press")
		}

		discoveryTopic := "homeassistant/device/" + player.Id + "/config"
		before := len(mqtt.Messages(discoveryTopic))
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed), 0x01})
		if len(mqtt.Messages(discoveryTopic)) != before {
			t.Error("event entity must only be registered once")
		}

		// Malformed message and unknown device are ignored.
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed)})
		bridge.handleUserControlPressed(gocec.Message{0x81, byte(gocec.OpcodeUserControlPressed), 0x00})
		if n := len(mqtt.Messages("cec2mqtt/Player/remote_key")); n != 2 {
			t.Errorf("published %d key events, want 2", n)
		}
	})
}

func TestParseAudioStatus(t *testing.T) {
	tests := []struct {
		operand     byte
		want        audioStatus
		volumeKnown bool
	}{
		{0x00, audioStatus{volume: 0}, true},
		{0x32, audioStatus{volume: 50}, true},
		{0x64, audioStatus{volume: 100}, true},
		{0xB2, audioStatus{volume: 50, muted: true}, true},
		{0x7F, audioStatus{volume: 127}, false},
		{0xFF, audioStatus{volume: 127, muted: true}, false},
	}

	for _, tt := range tests {
		got, known := parseAudioStatus(tt.operand)
		if got != tt.want || known != tt.volumeKnown {
			t.Errorf("parseAudioStatus(0x%02X) = %+v, %v; want %+v, %v", tt.operand, got, known, tt.want, tt.volumeKnown)
		}
	}
}

func newTestAudioBridge(t *testing.T) (*Audio, *fake.Cec, *fake.Mqtt, *device.Device) {
	t.Helper()
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newAudio(cec, mqtt)
	bridge.monitorIntervals = [3]time.Duration{time.Hour, time.Hour, time.Hour}
	bridge.haBridge = newTestDiscovery(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	receiver := fake.AddDevice(t, registry, gocec.DeviceAudiosystem, gocec.PhysicalAddress{0x10, 0}, "Receiver")

	return bridge, cec, mqtt, receiver
}

func TestAudioBridgeOnlyHandlesAudioSystem(t *testing.T) {
	_, _, mqtt, receiver := newTestAudioBridge(t)

	mqtt.Mux.Lock()
	for topic := range mqtt.Subscriptions {
		if strings.HasPrefix(topic, "homeassistant/") {
			continue // legacy discovery topics watched for migration
		}
		if topic != "cec2mqtt/Receiver/volume/set" && topic != "cec2mqtt/Receiver/mute/set" {
			t.Errorf("unexpected subscription %q", topic)
		}
	}
	mqtt.Mux.Unlock()

	_, components := mqtt.Discovery(t, receiver.Id)
	want := map[string]string{"volume": "sensor", "mute": "switch", "volume_up": "button", "volume_down": "button"}
	for id, platform := range want {
		if components[id]["platform"] != platform {
			t.Errorf("expected %s component %q, got %v", platform, id, components[id])
		}
	}
}

func TestAudioBridgeRequestsStatus(t *testing.T) {
	_, cec, _, _ := newTestAudioBridge(t)

	fake.WaitFor(t, "Give Audio Status", func() bool {
		return len(cec.Sent(gocec.OpcodeGiveAudioStatus)) > 0
	})
	assertMessages(t, cec.Sent(gocec.OpcodeGiveAudioStatus)[:1], []gocec.Message{{0x15, byte(gocec.OpcodeGiveAudioStatus)}})
}

func TestAudioBridgeReportAudioStatus(t *testing.T) {
	bridge, _, mqtt, _ := newTestAudioBridge(t)

	report := func(source byte, operand byte) {
		bridge.handleReportAudioStatus(gocec.Message{source<<4 | 0x01, byte(gocec.OpcodeReportAudioStatus), operand})
	}

	report(0x5, 0x19)
	if got := mqtt.Messages("cec2mqtt/Receiver/volume"); len(got) != 1 || got[0] != "25" {
		t.Errorf("volume published %q, want [25]", got)
	}
	if got := mqtt.Messages("cec2mqtt/Receiver/mute"); len(got) != 1 || got[0] != "off" {
		t.Errorf("mute published %q, want [off]", got)
	}

	// Unchanged status isn't published again, and reports by other devices are ignored.
	report(0x5, 0x19)
	report(0x0, 0x40)
	if got := mqtt.Messages("cec2mqtt/Receiver/volume"); len(got) != 1 {
		t.Errorf("volume published %q, want only one message", got)
	}

	// Muted with unknown volume: mute is published, volume is not.
	report(0x5, 0xFF)
	if got := mqtt.Messages("cec2mqtt/Receiver/mute"); len(got) != 2 || got[1] != "on" {
		t.Errorf("mute published %q, want [off on]", got)
	}
	if got := mqtt.Messages("cec2mqtt/Receiver/volume"); len(got) != 1 {
		t.Errorf("unknown volume must not be published, got %q", got)
	}
}

func TestAudioBridgeCommands(t *testing.T) {
	bridge, cec, mqtt, _ := newTestAudioBridge(t)

	keys := func() []string {
		var names []string
		for _, message := range cec.Sent(gocec.OpcodeUserControlPressed) {
			if message.Destination() != gocec.DeviceAudiosystem {
				t.Errorf("key sent to %s instead of the audio system", message.Destination())
			}
			names = append(names, KeyName(message.Parameters()[0]))
		}
		return names
	}

	mqtt.Deliver(t, "cec2mqtt/Receiver/volume/set", "up")
	mqtt.Deliver(t, "cec2mqtt/Receiver/volume/set", "DOWN")
	mqtt.Deliver(t, "cec2mqtt/Receiver/volume/set", "50") // not supported
	if got := keys(); len(got) != 2 || got[0] != "volume_up" || got[1] != "volume_down" {
		t.Fatalf("keys sent %q, want [volume_up volume_down]", got)
	}

	// Mute state unknown: on/off always presses the toggle.
	mqtt.Deliver(t, "cec2mqtt/Receiver/mute/set", "on")
	if got := keys(); len(got) != 3 || got[2] != "mute" {
		t.Fatalf("keys sent %q, want mute toggle", got)
	}

	// Known to be muted: "on" does nothing, "off" and "toggle" press the key.
	bridge.handleReportAudioStatus(gocec.Message{0x51, byte(gocec.OpcodeReportAudioStatus), 0x80 | 20})
	mqtt.Deliver(t, "cec2mqtt/Receiver/mute/set", "on")
	if got := keys(); len(got) != 3 {
		t.Fatalf("mute must not be toggled when already muted, keys sent %q", got)
	}
	mqtt.Deliver(t, "cec2mqtt/Receiver/mute/set", "off")
	mqtt.Deliver(t, "cec2mqtt/Receiver/mute/set", "toggle")
	if got := keys(); len(got) != 5 {
		t.Fatalf("keys sent %q, want 5 key presses", got)
	}
}

func TestSourceSelect(t *testing.T) {
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newActiveSource(cec, mqtt, registry)
	bridge.haBridge = newTestDiscovery(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	// The TV may be found after other devices.
	player := fake.AddDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")
	tv := fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	fake.AddDevice(t, registry, gocec.DevicePlaybackDevice2, gocec.PhysicalAddress{0x20, 0}, "Console")

	_, components := mqtt.Discovery(t, tv.Id)
	options, _ := components["source"]["options"].([]any)
	if fmt.Sprint(options) != "[Console Player TV]" {
		t.Errorf("unexpected source options %v", options)
	}
	if _, other := mqtt.Discovery(t, player.Id); other["source"] != nil {
		t.Error("only the TV must have a source select")
	}

	mqtt.Deliver(t, "cec2mqtt/TV/source/set", "Player")
	assertMessages(t, cec.Sent(), []gocec.Message{
		{0x10, byte(gocec.OpcodeImageViewOn)},
		{0x1F, byte(gocec.OpcodeSetStreamPath), 0x10, 0x00},
	})

	mqtt.Deliver(t, "cec2mqtt/TV/source/set", "Unknown")
	if len(cec.Sent()) != 2 {
		t.Error("unknown source must not be selected")
	}

	bridge.updateActiveSource(player)
	bridge.updateActiveSource(nil)
	if got := mqtt.Messages("cec2mqtt/TV/source"); fmt.Sprint(got) != "[Player None]" {
		t.Errorf("published sources %q, want [Player None]", got)
	}
}
