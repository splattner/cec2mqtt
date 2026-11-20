package main

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RobertMe/gocec"
)

func testAppConfig(homeAssistant bool) *Config {
	return &Config{
		Mqtt: MqttConfig{BaseTopic: "cec2mqtt"},
		HomeAssistant: HomeAssistantConfig{
			Enable:          homeAssistant,
			DiscoveryPrefix: "homeassistant",
			BirthTopic:      "homeassistant/status",
			BirthPayload:    "online",
		},
	}
}

// TestAppWiring checks that newApp connects every bridge to CEC, MQTT, the
// device registry and Home Assistant.
func TestAppWiring(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
		mqtt := newFakeMqtt()
		registry := newTestRegistry(t)

		app := newApp(testAppConfig(true), registry, mqtt, cec)
		defer app.Close()

		tv := addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		receiver := addTestDevice(t, registry, gocec.DeviceAudiosystem, gocec.PhysicalAddress{0x10, 0}, "Receiver")
		time.Sleep(time.Second) // discovery messages are batched for 500ms

		for _, topic := range []string{
			"homeassistant/status",
			"cec2mqtt/TV/power/set",
			"cec2mqtt/TV/key/set",
			"cec2mqtt/TV/is_active_source/set",
			"cec2mqtt/TV/source/set",
			"cec2mqtt/Receiver/power/set",
			"cec2mqtt/Receiver/volume/set",
			"cec2mqtt/Receiver/mute/set",
		} {
			mqtt.mux.Lock()
			_, ok := mqtt.subscriptions[topic]
			mqtt.mux.Unlock()
			if !ok {
				t.Errorf("not subscribed to %s", topic)
			}
		}

		wantComponents := map[*Device]map[string]string{
			tv:       {"power": "switch", "is_active_source": "binary_sensor", "source": "select", "remote_key": "event", "physical_address": "sensor"},
			receiver: {"power": "switch", "is_active_source": "binary_sensor", "volume": "sensor", "mute": "switch"},
		}
		for device, want := range wantComponents {
			_, components := mqtt.discovery(t, device.Id)
			for id, platform := range want {
				if components[id]["platform"] != platform {
					t.Errorf("%s: expected %s component %q, got %v", device.Config.MqttTopic, platform, id, components[id])
				}
			}
		}

		// CEC messages reach the bridges.
		cec.receive(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
		cec.receive(gocec.NewMessage(gocec.DeviceAudiosystem, gocec.DeviceRecodingDevice1, gocec.OpcodeReportAudioStatus, []byte{0x20}))
		synctest.Wait()
		if got := mqtt.messages(tvPowerTopic); len(got) == 0 || got[len(got)-1] != "on" {
			t.Errorf("TV power: got %q, want last to be on", got)
		}
		if got := mqtt.messages("cec2mqtt/Receiver/volume"); !slices.Equal(got, []string{"32"}) {
			t.Errorf("receiver volume: got %q, want [32]", got)
		}

		// A Home Assistant restart republishes the states.
		mqtt.reset()
		mqtt.deliver(t, "homeassistant/status", "online")
		synctest.Wait()
		if got := mqtt.messages(tvPowerTopic); !slices.Equal(got, []string{"on"}) {
			t.Errorf("TV power after Home Assistant birth: got %q, want [on]", got)
		}
		if got := mqtt.messages("cec2mqtt/TV/is_active_source"); len(got) != 1 {
			t.Errorf("TV active source after Home Assistant birth: got %q", got)
		}
	})
}

func TestAppWithoutHomeAssistant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
		mqtt := newFakeMqtt()
		registry := newTestRegistry(t)

		app := newApp(testAppConfig(false), registry, mqtt, cec)
		defer app.Close()

		if app.homeAssistant != nil {
			t.Fatal("Home Assistant bridge created while disabled")
		}

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		time.Sleep(time.Second)

		mqtt.mux.Lock()
		defer mqtt.mux.Unlock()
		if _, ok := mqtt.subscriptions["cec2mqtt/TV/power/set"]; !ok {
			t.Error("bridges not set up without Home Assistant")
		}
		for _, message := range mqtt.published {
			if len(message.topic) > len("homeassistant/") && message.topic[:len("homeassistant/")] == "homeassistant/" {
				t.Errorf("published to Home Assistant while disabled: %s", message.topic)
			}
		}
	})
}
