package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

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
	if _, components := mqtt.discovery(t, tv.Id); components["remote_key"]["platform"] != "event" {
		t.Error("expected event entity for the TV")
	}
	if _, components := mqtt.discovery(t, player.Id); components != nil {
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
		if _, components := mqtt.discovery(t, player.Id); components["remote_key"]["platform"] != "event" {
			t.Error("expected event entity to be registered on first key press")
		}

		discoveryTopic := "homeassistant/device/" + player.Id + "/config"
		before := len(mqtt.messages(discoveryTopic))
		bridge.handleUserControlPressed(gocec.Message{0x41, byte(gocec.OpcodeUserControlPressed), 0x01})
		if len(mqtt.messages(discoveryTopic)) != before {
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
	for topic := range mqtt.subscriptions {
		if strings.HasPrefix(topic, "homeassistant/") {
			continue // legacy discovery topics watched for migration
		}
		if topic != "cec2mqtt/Receiver/volume/set" && topic != "cec2mqtt/Receiver/mute/set" {
			t.Errorf("unexpected subscription %q", topic)
		}
	}
	mqtt.mux.Unlock()

	_, components := mqtt.discovery(t, receiver.Id)
	want := map[string]string{"volume": "sensor", "mute": "switch", "volume_up": "button", "volume_down": "button"}
	for id, platform := range want {
		if components[id]["platform"] != platform {
			t.Errorf("expected %s component %q, got %v", platform, id, components[id])
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

	ha.RegisterSwitch(device, "power")
	ha.RegisterButton(device, "volume_up", "volume", "up", "")
	ha.RegisterSelect(device, "source", []string{"Player", "TV"})
	ha.RegisterEvent(device, "remote_key", []string{"select", "up"})

	payload, components := mqtt.discovery(t, "abc")
	if payload == nil {
		t.Fatal("no device discovery message published")
	}

	dev, _ := payload["device"].(map[string]any)
	if ids, _ := dev["identifiers"].([]any); len(ids) != 1 || ids[0] != "cec2mqtt_abc" || dev["name"] != "TV" {
		t.Errorf("unexpected device %v", dev)
	}
	if origin, _ := payload["origin"].(map[string]any); origin["name"] != "cec2mqtt" {
		t.Errorf("unexpected origin %v", payload["origin"])
	}
	if payload["availability_topic"] != "cec2mqtt/state" || payload["payload_available"] != "online" {
		t.Errorf("expected shared availability, got %v", payload)
	}
	if len(components) != 4 {
		t.Fatalf("expected 4 components, got %v", components)
	}

	// unique_ids must match earlier versions, so entities are migrated instead of recreated.
	power := components["power"]
	if power["platform"] != "switch" || power["unique_id"] != "abc_power_cec2mqtt" || power["name"] != "Power" ||
		power["state_topic"] != "cec2mqtt/TV/power" || power["command_topic"] != "cec2mqtt/TV/power/set" {
		t.Errorf("unexpected power component %v", power)
	}

	button := components["volume_up"]
	if _, ok := button["state_topic"]; ok {
		t.Error("button must not have a state topic")
	}
	if button["command_topic"] != "cec2mqtt/TV/volume/set" || button["payload_press"] != "up" {
		t.Errorf("unexpected button component %v", button)
	}

	source := components["source"]
	if source["platform"] != "select" || source["command_topic"] != "cec2mqtt/TV/source/set" {
		t.Errorf("unexpected select component %v", source)
	}
	if options, _ := source["options"].([]any); len(options) != 2 {
		t.Errorf("unexpected select options %v", source["options"])
	}

	event := components["remote_key"]
	if event["platform"] != "event" || event["state_topic"] != "cec2mqtt/TV/remote_key" {
		t.Errorf("unexpected event component %v", event)
	}
	if types, _ := event["event_types"].([]any); len(types) != 2 {
		t.Errorf("unexpected event types %v", event["event_types"])
	}

	for _, config := range components {
		if _, ok := config["device"]; ok {
			t.Errorf("device must only be set at the root, got %v", config)
		}
	}
}

func TestHomeAssistantMigration(t *testing.T) {
	mqtt := newFakeMqtt()
	ha := newTestHomeAssistantBridge(mqtt)
	device := &Device{
		Id:        "abc",
		Config:    &DeviceConfig{Id: "abc", MqttTopic: "TV"},
		CecDevice: &CecDeviceDescription{OSD: "TV", vendor: gocec.VendorSamsung},
	}

	ha.RegisterSwitch(device, "power")

	legacy := "homeassistant/switch/abc/power/config"
	removed := "homeassistant/button/abc/select_source/config"
	discoveryTopic := "homeassistant/device/abc/config"

	// Nothing retained on the legacy topics: nothing is migrated or removed.
	mqtt.deliver(t, legacy, "")
	mqtt.deliver(t, removed, "")
	if len(mqtt.messages(legacy)) != 0 || len(mqtt.messages(removed)) != 0 {
		t.Fatal("expected no messages on legacy topics without retained configuration")
	}

	// A retained single component configuration of an earlier version is migrated.
	mqtt.reset()
	mqtt.deliver(t, legacy, `{"name":"Power","unique_id":"abc_power_cec2mqtt"}`)

	mqtt.mux.Lock()
	var sequence []string
	for _, p := range mqtt.published {
		if !p.retained {
			t.Errorf("discovery message on %s must be retained", p.topic)
		}
		sequence = append(sequence, p.topic+" "+p.payload)
	}
	mqtt.mux.Unlock()

	if len(sequence) != 3 ||
		sequence[0] != legacy+" "+migrateDiscoveryPayload ||
		!strings.HasPrefix(sequence[1], discoveryTopic+" ") ||
		sequence[2] != legacy+" " {
		t.Errorf("unexpected migration sequence:\n%s", strings.Join(sequence, "\n"))
	}

	// Our own migration message and the cleanup are delivered back and ignored.
	mqtt.reset()
	mqtt.deliver(t, legacy, migrateDiscoveryPayload)
	mqtt.deliver(t, legacy, "")
	if len(mqtt.published) != 0 {
		t.Errorf("unexpected messages %v", mqtt.published)
	}

	// Removed components are cleared.
	mqtt.deliver(t, removed, `{"name":"Select source"}`)
	if got := mqtt.messages(removed); len(got) != 1 || got[0] != "" {
		t.Errorf("expected removed component to be cleared, got %q", got)
	}
}

func TestSourceSelect(t *testing.T) {
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newActiveSourceBridge(cec, mqtt, registry)
	bridge.haBridge = newTestHomeAssistantBridge(mqtt)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	// The TV may be found after other devices.
	player := addTestDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")
	tv := addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	addTestDevice(t, registry, gocec.DevicePlaybackDevice2, gocec.PhysicalAddress{0x20, 0}, "Console")

	_, components := mqtt.discovery(t, tv.Id)
	options, _ := components["source"]["options"].([]any)
	if fmt.Sprint(options) != "[Console Player TV]" {
		t.Errorf("unexpected source options %v", options)
	}
	if _, other := mqtt.discovery(t, player.Id); other["source"] != nil {
		t.Error("only the TV must have a source select")
	}

	mqtt.deliver(t, "cec2mqtt/TV/source/set", "Player")
	assertMessages(t, cec.sent(), []gocec.Message{
		{0x10, byte(gocec.OpcodeImageViewOn)},
		{0x1F, byte(gocec.OpcodeSetStreamPath), 0x10, 0x00},
	})

	mqtt.deliver(t, "cec2mqtt/TV/source/set", "Unknown")
	if len(cec.sent()) != 2 {
		t.Error("unknown source must not be selected")
	}

	bridge.updateActiveSource(player)
	bridge.updateActiveSource(nil)
	if got := mqtt.messages("cec2mqtt/TV/source"); fmt.Sprint(got) != "[Player None]" {
		t.Errorf("published sources %q, want [Player None]", got)
	}
}
