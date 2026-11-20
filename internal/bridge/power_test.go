package bridge

import (
	"testing"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/fake"
	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestPowerStateValue(t *testing.T) {
	tests := map[gocec.PowerStatus]string{
		gocec.PowerStatusOn:                  "on",
		gocec.PowerStatusTransitionToOn:      "on",
		gocec.PowerStatusStandBy:             "off",
		gocec.PowerStatusTransitionToStandby: "off",
		gocec.PowerStatus(0x99):              "unknown",
	}

	for status, want := range tests {
		if got := powerStateValue(status); got != want {
			t.Errorf("powerStateValue(%v) = %q, want %q", status, got, want)
		}
	}
}

func TestPowerStatusRequest(t *testing.T) {
	tv := &device.Device{LogicalAddress: gocec.DeviceTV}
	player := &device.Device{LogicalAddress: gocec.DevicePlaybackDevice1}

	// Always sent from the adapter's own address, which the Linux kernel CEC
	// API requires. Earlier versions used the broadcast address (for the TV)
	// or the TV's address, which the kernel rejects with EINVAL.
	assertMessages(t, []gocec.Message{
		powerStatusRequest(gocec.DeviceRecodingDevice1, tv),
		powerStatusRequest(gocec.DeviceRecodingDevice1, player),
	}, []gocec.Message{
		{0x10, byte(gocec.OpcodeGiveDevicePowerStatus)},
		{0x14, byte(gocec.OpcodeGiveDevicePowerStatus)},
	})
}

func TestActiveSourceNotCheckedWhileTVInStandby(t *testing.T) {
	cec := fake.NewCec(gocec.DeviceRecodingDevice1)
	mqtt := fake.NewMqtt()
	registry := fake.NewRegistry(t)
	bridge := newActiveSource(cec, mqtt, registry)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)

	tv := fake.AddDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
	player := fake.AddDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")
	bridge.updateActiveSource(player)

	bridge.mux.Lock()
	bridge.allowedSources[tv.LogicalAddress] = false
	bridge.mux.Unlock()

	bridge.checkActiveSource()

	if got := mqtt.Messages("cec2mqtt/Player/is_active_source"); len(got) != 2 || got[1] != "off" {
		t.Errorf("expected active source to be cleared, got %q", got)
	}
	if cec.ActiveSourceRequests != 0 {
		t.Errorf("active source requested %d times while the TV is in standby", cec.ActiveSourceRequests)
	}
}
