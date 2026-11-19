package main

import (
	"testing"

	"github.com/RobertMe/gocec"
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
	tv := &Device{LogicalAddress: gocec.DeviceTV}
	player := &Device{LogicalAddress: gocec.DevicePlaybackDevice1}

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
