package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RobertMe/gocec"
)

func newTestRegistry(t *testing.T, configs ...*DeviceConfig) *DeviceRegistry {
	t.Helper()
	registry := NewDeviceRegistry(t.TempDir() + "/")
	for _, config := range configs {
		registry.configDevices[config.Id] = config
	}
	return registry
}

func TestFindDevice(t *testing.T) {
	tv := func() *DeviceConfig {
		return &DeviceConfig{Id: "tv", PhysicalAddress: "0.0.0.0", VendorId: 0xF0, OSD: "TV", MqttTopic: "tv"}
	}
	player := func() *DeviceConfig {
		return &DeviceConfig{Id: "player", PhysicalAddress: "1.0.0.0", VendorId: 0xF0, OSD: "Player", MqttTopic: "player"}
	}

	tests := []struct {
		name            string
		configs         []*DeviceConfig
		physicalAddress string
		vendor          int
		osd             string
		wantId          string // empty means a new device must be created
	}{
		{"exact match", []*DeviceConfig{tv(), player()}, "1.0.0.0", 0xF0, "Player", "player"},
		{"moved to other HDMI port", []*DeviceConfig{tv(), player()}, "2.0.0.0", 0xF0, "Player", "player"},
		{"renamed OSD", []*DeviceConfig{tv(), player()}, "1.0.0.0", 0xF0, "Player 2", "player"},
		{"different vendor", []*DeviceConfig{player()}, "1.0.0.0", 0x01, "Player", ""},
		{"unknown device", []*DeviceConfig{tv()}, "3.0.0.0", 0xF0, "Console", ""},
		{"ambiguous partial matches", []*DeviceConfig{
			{Id: "a", PhysicalAddress: "1.0.0.0", VendorId: 0xF0, OSD: "A"},
			{Id: "b", PhysicalAddress: "2.0.0.0", VendorId: 0xF0, OSD: "Player"},
		}, "1.0.0.0", 0xF0, "Player", ""},
		{"exact match wins over partial matches", []*DeviceConfig{
			{Id: "a", PhysicalAddress: "1.0.0.0", VendorId: 0xF0, OSD: "A"},
			{Id: "b", PhysicalAddress: "2.0.0.0", VendorId: 0xF0, OSD: "Player"},
			{Id: "c", PhysicalAddress: "1.0.0.0", VendorId: 0xF0, OSD: "B"},
			{Id: "exact", PhysicalAddress: "1.0.0.0", VendorId: 0xF0, OSD: "Player"},
		}, "1.0.0.0", 0xF0, "Player", "exact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Map iteration order is random, so repeat to catch order dependent behaviour.
			for i := 0; i < 20; i++ {
				registry := newTestRegistry(t, tt.configs...)
				known := len(registry.configDevices)

				got := registry.FindDevice(tt.physicalAddress, tt.vendor, tt.osd)

				if tt.wantId != "" && got.Id != tt.wantId {
					t.Fatalf("got device %q, want %q", got.Id, tt.wantId)
				}

				if tt.wantId == "" {
					if len(registry.configDevices) != known+1 {
						t.Fatalf("expected a new device to be created, got %q", got.Id)
					}
					if got.MqttTopic != tt.osd {
						t.Errorf("new device MqttTopic = %q, want %q", got.MqttTopic, tt.osd)
					}
				}

				if got.PhysicalAddress != tt.physicalAddress || got.VendorId != tt.vendor || got.OSD != tt.osd {
					t.Errorf("device not updated to current values: %+v", got)
				}
			}
		})
	}
}

func TestGetByCecDevice(t *testing.T) {
	registry := newTestRegistry(t)

	var added []*Device
	registry.RegisterDeviceAddedHandler(func(device *Device) {
		added = append(added, device)
	})

	creatorCalls := 0
	creator := func(description CecDeviceDescription) CreateCecDeviceDescription {
		return func() *CecDeviceDescription {
			creatorCalls++
			return &description
		}
	}

	player := CecDeviceDescription{
		logicalAddress:  gocec.DevicePlaybackDevice1,
		physicalAddress: gocec.PhysicalAddress{0x10, 0x00},
		vendor:          gocec.VendorSamsung,
		OSD:             "Player",
	}

	device := registry.GetByCecDevice(player.logicalAddress, creator(player))
	if device == nil {
		t.Fatal("expected device to be created")
	}
	if len(added) != 1 || added[0] != device {
		t.Fatalf("expected device added handler to be called once, got %d calls", len(added))
	}

	// The new device must be persisted right away.
	data, err := os.ReadFile(filepath.Join(registry.dataDirectory, "devices.yaml"))
	if err != nil {
		t.Fatalf("devices.yaml not written: %v", err)
	}
	reloaded := loadDevicesFromConfig(registry.dataDirectory)
	if reloaded[device.Id] == nil || reloaded[device.Id].OSD != "Player" {
		t.Errorf("device not persisted correctly, devices.yaml:\n%s", data)
	}

	// A second lookup is served from the registry.
	if again := registry.GetByCecDevice(player.logicalAddress, creator(player)); again != device {
		t.Error("expected the same device on second lookup")
	}
	if creatorCalls != 1 {
		t.Errorf("creator called %d times, want 1", creatorCalls)
	}
	if len(added) != 1 {
		t.Errorf("device added handler called again")
	}
	if found := registry.FindByLogicalAddress(player.logicalAddress); found != device {
		t.Error("FindByLogicalAddress did not return the device")
	}
}

func TestGetByCecDeviceIgnoresIncompleteDevices(t *testing.T) {
	tests := map[string]CecDeviceDescription{
		"no physical address": {physicalAddress: gocec.PhysicalAddress{0xFF, 0xFF}, vendor: gocec.VendorSamsung, OSD: "X"},
		"unknown vendor":      {physicalAddress: gocec.PhysicalAddress{0x10, 0x00}, vendor: gocec.VendorUnknown, OSD: "X"},
		"cec2mqtt itself":     {physicalAddress: gocec.PhysicalAddress{0x10, 0x00}, vendor: gocec.VendorSamsung, OSD: "cec2mqtt"},
	}

	for name, description := range tests {
		t.Run(name, func(t *testing.T) {
			registry := newTestRegistry(t)
			description := description
			if device := registry.GetByCecDevice(gocec.DevicePlaybackDevice1, func() *CecDeviceDescription { return &description }); device != nil {
				t.Errorf("expected device to be ignored, got %+v", device)
			}
		})
	}
}

func TestGetByCecDeviceIgnoredInConfig(t *testing.T) {
	registry := newTestRegistry(t, &DeviceConfig{
		Id: "player", PhysicalAddress: "1.0.0.0", VendorId: int(gocec.VendorSamsung), OSD: "Player", Ignore: true,
	})

	added := 0
	registry.RegisterDeviceAddedHandler(func(*Device) { added++ })

	description := CecDeviceDescription{
		logicalAddress:  gocec.DevicePlaybackDevice1,
		physicalAddress: gocec.PhysicalAddress{0x10, 0x00},
		vendor:          gocec.VendorSamsung,
		OSD:             "Player",
	}
	creator := func() *CecDeviceDescription { return &description }

	if device := registry.GetByCecDevice(description.logicalAddress, creator); device != nil {
		t.Error("expected ignored device to return nil")
	}
	if device := registry.GetByCecDevice(description.logicalAddress, creator); device != nil {
		t.Error("expected ignored device to return nil on second lookup")
	}
	if registry.FindByLogicalAddress(description.logicalAddress) != nil {
		t.Error("expected FindByLogicalAddress to skip ignored device")
	}
	if added != 0 {
		t.Errorf("device added handler called %d times for ignored device", added)
	}
}
