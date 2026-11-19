package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/RobertMe/gocec"
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
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	sendKey(cec, gocec.DevicePlaybackDevice1, 0x44)

	want := []gocec.Message{{0x14, 0x44, 0x44}, {0x14, 0x45}}
	assertMessages(t, cec.sent(), want)
}

func TestSelectSource(t *testing.T) {
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	device := &Device{CecDevice: &CecDeviceDescription{physicalAddress: gocec.PhysicalAddress{0x21, 0x00}}}

	selectSource(cec, device)

	want := []gocec.Message{
		{0x10, byte(gocec.OpcodeImageViewOn)},
		{0x1F, byte(gocec.OpcodeSetStreamPath), 0x21, 0x00},
	}
	assertMessages(t, cec.sent(), want)
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
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newRemoteBridge(cec, mqtt, registry)
	bridge.haBridge = newTestHomeAssistantBridge(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	tv := addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	player := addTestDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")

	// Event entity of the TV is registered right away, others only on the first key press.
	if len(mqtt.messages("homeassistant/event/"+tv.Id+"/remote_key/config")) != 1 {
		t.Error("expected event entity for the TV")
	}
	if len(mqtt.messages("homeassistant/event/"+player.Id+"/remote_key/config")) != 0 {
		t.Error("expected no event entity for the player yet")
	}

	t.Run("send key", func(t *testing.T) {
		mqtt.deliver(t, "cec2mqtt/Player/key/set", " Play\n")
		assertMessages(t, cec.sent(), []gocec.Message{{0x14, 0x44, 0x44}, {0x14, 0x45}})

		mqtt.deliver(t, "cec2mqtt/Player/key/set", "no_such_key")
		if len(cec.sent()) != 2 {
			t.Error("unknown key must not be sent")
		}
	})

	t.Run("receive key", func(t *testing.T) {
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed), 0x00})

		got := mqtt.messages("cec2mqtt/Player/remote_key")
		if len(got) != 1 || got[0] != `{"event_type":"select"}` {
			t.Errorf("published %q", got)
		}
		if len(mqtt.messages("homeassistant/event/"+player.Id+"/remote_key/config")) != 1 {
			t.Error("expected event entity to be registered on first key press")
		}

		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed), 0x01})
		if len(mqtt.messages("homeassistant/event/"+player.Id+"/remote_key/config")) != 1 {
			t.Error("event entity must only be registered once")
		}

		// Malformed message and unknown device are ignored.
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed)})
		bridge.handleUserControlPressed(gocec.Message{0x81, byte(gocec.OpcodeUserControlPressed), 0x00})
		if n := len(mqtt.messages("cec2mqtt/Player/remote_key")); n != 2 {
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

func newTestAudioBridge(t *testing.T) (*AudioBridge, *fakeCec, *fakeMqtt, *Device) {
	t.Helper()
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newAudioBridge(cec, mqtt)
	bridge.monitorIntervals = [3]time.Duration{time.Hour, time.Hour, time.Hour}
	bridge.haBridge = newTestHomeAssistantBridge(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	receiver := addTestDevice(t, registry, gocec.DeviceAudiosystem, gocec.PhysicalAddress{0x10, 0}, "Receiver")

	return bridge, cec, mqtt, receiver
}

func TestAudioBridgeOnlyHandlesAudioSystem(t *testing.T) {
	_, _, mqtt, receiver := newTestAudioBridge(t)

	mqtt.mux.Lock()
	defer mqtt.mux.Unlock()
	for topic := range mqtt.subscriptions {
		if topic != "cec2mqtt/Receiver/volume/set" && topic != "cec2mqtt/Receiver/mute/set" {
			t.Errorf("unexpected subscription %q", topic)
		}
	}

	for _, entity := range []string{"sensor/%s/volume", "switch/%s/mute", "button/%s/volume_up", "button/%s/volume_down"} {
		topic := "homeassistant/" + fmt.Sprintf(entity, receiver.Id) + "/config"
		found := false
		for _, p := range mqtt.published {
			found = found || p.topic == topic
		}
		if !found {
			t.Errorf("expected Home Assistant entity %s", topic)
		}
	}
}

func TestAudioBridgeRequestsStatus(t *testing.T) {
	_, cec, _, _ := newTestAudioBridge(t)

	waitFor(t, "Give Audio Status", func() bool {
		return len(cec.sent(gocec.OpcodeGiveAudioStatus)) > 0
	})
	assertMessages(t, cec.sent(gocec.OpcodeGiveAudioStatus)[:1], []gocec.Message{{0x15, byte(gocec.OpcodeGiveAudioStatus)}})
}

func TestAudioBridgeReportAudioStatus(t *testing.T) {
	bridge, _, mqtt, _ := newTestAudioBridge(t)

	report := func(source byte, operand byte) {
		bridge.handleReportAudioStatus(gocec.Message{source<<4 | 0x01, byte(gocec.OpcodeReportAudioStatus), operand})
	}

	report(0x5, 0x19)
	if got := mqtt.messages("cec2mqtt/Receiver/volume"); len(got) != 1 || got[0] != "25" {
		t.Errorf("volume published %q, want [25]", got)
	}
	if got := mqtt.messages("cec2mqtt/Receiver/mute"); len(got) != 1 || got[0] != "off" {
		t.Errorf("mute published %q, want [off]", got)
	}

	// Unchanged status isn't published again, and reports by other devices are ignored.
	report(0x5, 0x19)
	report(0x0, 0x40)
	if got := mqtt.messages("cec2mqtt/Receiver/volume"); len(got) != 1 {
		t.Errorf("volume published %q, want only one message", got)
	}

	// Muted with unknown volume: mute is published, volume is not.
	report(0x5, 0xFF)
	if got := mqtt.messages("cec2mqtt/Receiver/mute"); len(got) != 2 || got[1] != "on" {
		t.Errorf("mute published %q, want [off on]", got)
	}
	if got := mqtt.messages("cec2mqtt/Receiver/volume"); len(got) != 1 {
		t.Errorf("unknown volume must not be published, got %q", got)
	}
}

func TestAudioBridgeCommands(t *testing.T) {
	bridge, cec, mqtt, _ := newTestAudioBridge(t)

	keys := func() []string {
		var names []string
		for _, message := range cec.sent(gocec.OpcodeUserControlPressed) {
			if message.Destination() != gocec.DeviceAudiosystem {
				t.Errorf("key sent to %s instead of the audio system", message.Destination())
			}
			names = append(names, KeyName(message.Parameters()[0]))
		}
		return names
	}

	mqtt.deliver(t, "cec2mqtt/Receiver/volume/set", "up")
	mqtt.deliver(t, "cec2mqtt/Receiver/volume/set", "DOWN")
	mqtt.deliver(t, "cec2mqtt/Receiver/volume/set", "50") // not supported
	if got := keys(); len(got) != 2 || got[0] != "volume_up" || got[1] != "volume_down" {
		t.Fatalf("keys sent %q, want [volume_up volume_down]", got)
	}

	// Mute state unknown: on/off always presses the toggle.
	mqtt.deliver(t, "cec2mqtt/Receiver/mute/set", "on")
	if got := keys(); len(got) != 3 || got[2] != "mute" {
		t.Fatalf("keys sent %q, want mute toggle", got)
	}

	// Known to be muted: "on" does nothing, "off" and "toggle" press the key.
	bridge.handleReportAudioStatus(gocec.Message{0x51, byte(gocec.OpcodeReportAudioStatus), 0x80 | 20})
	mqtt.deliver(t, "cec2mqtt/Receiver/mute/set", "on")
	if got := keys(); len(got) != 3 {
		t.Fatalf("mute must not be toggled when already muted, keys sent %q", got)
	}
	mqtt.deliver(t, "cec2mqtt/Receiver/mute/set", "off")
	mqtt.deliver(t, "cec2mqtt/Receiver/mute/set", "toggle")
	if got := keys(); len(got) != 5 {
		t.Fatalf("keys sent %q, want 5 key presses", got)
	}
}

func TestHomeAssistantDiscovery(t *testing.T) {
	mqtt := newFakeMqtt()
	ha := newTestHomeAssistantBridge(mqtt)
	ha.config.Mqtt.StateTopic = "cec2mqtt/state"
	ha.config.Mqtt.BirthMessage = "online"
	ha.config.Mqtt.WillMessage = "offline"

	device := &Device{
		Id:        "abc",
		Config:    &DeviceConfig{Id: "abc", MqttTopic: "TV"},
		CecDevice: &CecDeviceDescription{OSD: "TV", vendor: gocec.VendorSamsung},
	}

	ha.RegisterButton(device, "select_source", "is_active_source", "on")
	ha.RegisterEvent(device, "remote_key", []string{"select", "up"})

	var button map[string]any
	decode(t, mqtt.messages("homeassistant/button/abc/select_source/config"), &button)
	if _, ok := button["state_topic"]; ok {
		t.Error("button must not have a state topic")
	}
	if button["command_topic"] != "cec2mqtt/TV/is_active_source/set" || button["payload_press"] != "on" {
		t.Errorf("unexpected button config %v", button)
	}
	if button["name"] != "Select source" || button["unique_id"] != "abc_select_source_cec2mqtt" {
		t.Errorf("unexpected button name/unique_id %v", button)
	}
	if button["availability_topic"] != "cec2mqtt/state" {
		t.Errorf("expected availability topic, got %v", button)
	}

	var event map[string]any
	decode(t, mqtt.messages("homeassistant/event/abc/remote_key/config"), &event)
	if event["state_topic"] != "cec2mqtt/TV/remote_key" {
		t.Errorf("unexpected event state topic %v", event["state_topic"])
	}
	if types, _ := event["event_types"].([]any); len(types) != 2 {
		t.Errorf("unexpected event types %v", event["event_types"])
	}
}

func decode(t *testing.T, messages []string, v any) {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("expected 1 discovery message, got %d", len(messages))
	}
	if err := json.Unmarshal([]byte(messages[0]), v); err != nil {
		t.Fatal(err)
	}
}
