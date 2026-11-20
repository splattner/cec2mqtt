package homeassistant

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestHomeAssistantCloseDropsPendingDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mqtt := fake.NewMqtt()
		bridge := newTestDiscovery(mqtt)
		bridge.PublishDelay = 500 * time.Millisecond
		registry := fake.NewRegistry(t)
		tv := fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")

		bridge.RegisterSwitch(tv, "power")
		bridge.Close()
		bridge.RegisterSwitch(tv, "power") // registering after Close doesn't publish either
		time.Sleep(time.Second)

		if payload, _ := mqtt.Discovery(t, tv.Id); payload != nil {
			t.Errorf("discovery published after Close: %v", payload)
		}
	})
}
