package homeassistant

import (
	"strings"
	"testing"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestHomeAssistantDiscovery(t *testing.T) {
	mqtt := fake.NewMqtt()
	ha := newTestDiscovery(mqtt)
	ha.config.Mqtt.StateTopic = "cec2mqtt/state"
	ha.config.Mqtt.BirthMessage = "online"
	ha.config.Mqtt.WillMessage = "offline"

	device := &device.Device{
		Id:        "abc",
		Config:    &device.Config{Id: "abc", MqttTopic: "TV"},
		CecDevice: &device.Description{OSD: "TV", Vendor: gocec.VendorSamsung},
	}

	ha.RegisterSwitch(device, "power")
	ha.RegisterButton(device, "volume_up", "volume", "up", "")
	ha.RegisterSelect(device, "source", []string{"Player", "TV"})
	ha.RegisterEvent(device, "remote_key", []string{"select", "up"})

	payload, components := mqtt.Discovery(t, "abc")
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
	mqtt := fake.NewMqtt()
	ha := newTestDiscovery(mqtt)
	device := &device.Device{
		Id:        "abc",
		Config:    &device.Config{Id: "abc", MqttTopic: "TV"},
		CecDevice: &device.Description{OSD: "TV", Vendor: gocec.VendorSamsung},
	}

	ha.RegisterSwitch(device, "power")

	legacy := "homeassistant/switch/abc/power/config"
	removed := "homeassistant/button/abc/select_source/config"
	discoveryTopic := "homeassistant/device/abc/config"

	// Nothing retained on the legacy topics: nothing is migrated or removed.
	mqtt.Deliver(t, legacy, "")
	mqtt.Deliver(t, removed, "")
	if len(mqtt.Messages(legacy)) != 0 || len(mqtt.Messages(removed)) != 0 {
		t.Fatal("expected no messages on legacy topics without retained configuration")
	}

	// A retained single component configuration of an earlier version is migrated.
	mqtt.Reset()
	mqtt.Deliver(t, legacy, `{"name":"Power","unique_id":"abc_power_cec2mqtt"}`)

	mqtt.Mux.Lock()
	var sequence []string
	for _, p := range mqtt.Published {
		if !p.Retained {
			t.Errorf("discovery message on %s must be retained", p.Topic)
		}
		sequence = append(sequence, p.Topic+" "+p.Payload)
	}
	mqtt.Mux.Unlock()

	if len(sequence) != 3 ||
		sequence[0] != legacy+" "+migrateDiscoveryPayload ||
		!strings.HasPrefix(sequence[1], discoveryTopic+" ") ||
		sequence[2] != legacy+" " {
		t.Errorf("unexpected migration sequence:\n%s", strings.Join(sequence, "\n"))
	}

	// Our own migration message and the cleanup are delivered back and ignored.
	mqtt.Reset()
	mqtt.Deliver(t, legacy, migrateDiscoveryPayload)
	mqtt.Deliver(t, legacy, "")
	if len(mqtt.Published) != 0 {
		t.Errorf("unexpected messages %v", mqtt.Published)
	}

	// Removed components are cleared.
	mqtt.Deliver(t, removed, `{"name":"Select source"}`)
	if got := mqtt.Messages(removed); len(got) != 1 || got[0] != "" {
		t.Errorf("expected removed component to be cleared, got %q", got)
	}
}
