package device

import (
	"testing"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

func TestDeviceCantUseReservedTopic(t *testing.T) {
	registry := newTestRegistry(t)
	description := &Description{
		LogicalAddress:  gocec.DevicePlaybackDevice1,
		PhysicalAddress: gocec.PhysicalAddress{0x10, 0},
		Vendor:          gocec.VendorSamsung,
		OSD:             ReservedTopic,
	}
	device := registry.GetByCecDevice(description.LogicalAddress, func() *Description { return description })
	if device == nil {
		t.Fatal("device not added")
	}
	if device.Config.MqttTopic != "bridge_device" {
		t.Errorf("MqttTopic = %q, want bridge_device", device.Config.MqttTopic)
	}
}
