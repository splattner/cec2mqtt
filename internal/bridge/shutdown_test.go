package bridge

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestBridgesStopPollingOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cec := fake.NewCec(gocec.DeviceRecodingDevice1)
		mqtt := fake.NewMqtt()
		registry := fake.NewRegistry(t)

		audio := newAudio(cec, mqtt)
		registry.RegisterDeviceAddedHandler(audio.deviceAdded)
		power := newPower(cec, mqtt, registry)
		registry.RegisterDeviceAddedHandler(power.deviceAdded)
		activeSource := newActiveSource(cec, mqtt, registry)
		activeSource.monitor = CreateMonitor(func() {}, activeSource.checkActiveSource, time.Minute, time.Second, time.Minute)
		registry.RegisterDeviceAddedHandler(activeSource.deviceAdded)

		fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		fake.AddDevice(t, registry, gocec.DeviceAudiosystem, gocec.PhysicalAddress{0x10, 0}, "Receiver")
		time.Sleep(time.Hour)

		audio.Close()
		power.Close()
		activeSource.Close()

		sent := len(cec.Sent())
		cec.Mux.Lock()
		activeSourceRequests := cec.ActiveSourceRequests
		cec.Mux.Unlock()

		// Every goroutine of the bridges has ended, otherwise synctest.Test
		// would fail when this function returns.
		time.Sleep(time.Hour)

		if got := len(cec.Sent()); got != sent {
			t.Errorf("%d CEC messages sent after Close", got-sent)
		}
		cec.Mux.Lock()
		defer cec.Mux.Unlock()
		if cec.ActiveSourceRequests != activeSourceRequests {
			t.Errorf("active source requested after Close")
		}
	})
}
