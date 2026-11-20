package bridge

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
)

// diagnosticProperties are the diagnostic values published per device, in
// <device>/<property>, and their Home Assistant icons.
var diagnosticProperties = []struct{ property, icon string }{
	{"physical_address", "mdi:hdmi-port"},
	{"logical_address", "mdi:identifier"},
	{"vendor", "mdi:factory"},
}

// Diagnostics publishes how each device is connected to the CEC bus. In
// Home Assistant these are diagnostic sensors, which are hidden from the
// default dashboards but help to find out which device is which.
type Diagnostics struct {
	mqtt     MQTT
	haBridge *homeassistant.Discovery

	mux     sync.Mutex
	devices map[string]*device.Device
}

func newDiagnostics(mqtt MQTT) *Diagnostics {
	return &Diagnostics{
		mqtt:    mqtt,
		devices: make(map[string]*device.Device),
	}
}

// setupDiagnostics creates the bridge and connects it to MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupDiagnostics(mqtt MQTT, devices *device.Registry, haBridge *homeassistant.Discovery) *Diagnostics {
	bridge := newDiagnostics(mqtt)

	if haBridge != nil {
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)
	mqtt.RegisterConnectedHandler(bridge.resendAll)

	return bridge
}

func (bridge *Diagnostics) deviceAdded(device *device.Device) {
	bridge.mux.Lock()
	bridge.devices[device.Id] = device
	bridge.mux.Unlock()

	bridge.registerEntities(device)
	bridge.publish(device)
}

func (bridge *Diagnostics) registerEntities(device *device.Device) {
	if bridge.haBridge == nil {
		return
	}

	for _, diagnostic := range diagnosticProperties {
		bridge.haBridge.RegisterDiagnosticSensor(device, diagnostic.property, diagnostic.icon)
	}
}

func (bridge *Diagnostics) publish(device *device.Device) {
	values := map[string]string{
		"physical_address": device.CecDevice.PhysicalAddress.String(),
		"logical_address":  logicalAddressName(device.LogicalAddress),
		"vendor":           device.CecDevice.Vendor.String(),
	}

	slog.Debug("Publishing diagnostics", "device.id", device.Id, "values", values)

	for _, diagnostic := range diagnosticProperties {
		bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, diagnostic.property), 0, false, values[diagnostic.property])
	}
}

func (bridge *Diagnostics) resendAll() {
	bridge.mux.Lock()
	devices := make([]*device.Device, 0, len(bridge.devices))
	for _, device := range bridge.devices {
		devices = append(devices, device)
	}
	bridge.mux.Unlock()

	for _, device := range devices {
		bridge.registerEntities(device)
		bridge.publish(device)
	}
}

// logicalAddressNames are the names of the logical addresses, as in the CEC
// specification. (libcec's names are abbreviated, like "Playback 1".)
var logicalAddressNames = map[gocec.LogicalAddress]string{
	gocec.DeviceTV:              "TV",
	gocec.DeviceRecodingDevice1: "Recording device 1",
	gocec.DeviceRecodingDevice2: "Recording device 2",
	gocec.DeviceTuner1:          "Tuner 1",
	gocec.DevicePlaybackDevice1: "Playback device 1",
	gocec.DeviceAudiosystem:     "Audio system",
	gocec.DeviceTuner2:          "Tuner 2",
	gocec.DeviceTuner3:          "Tuner 3",
	gocec.DevicePlaybackDevice2: "Playback device 2",
	gocec.DeviceRecodingDevice3: "Recording device 3",
	gocec.DeviceTuner4:          "Tuner 4",
	gocec.DevicePlaybackDevice3: "Playback device 3",
	gocec.DeviceReserved1:       "Reserved 1",
	gocec.DeviceReserved2:       "Reserved 2",
	gocec.DeviceFreeUse:         "Free use",
	gocec.DeviceBroadcast:       "Broadcast",
}

// logicalAddressName returns the name and number of the logical address, like
// "Playback device 1 (4)".
func logicalAddressName(address gocec.LogicalAddress) string {
	name, ok := logicalAddressNames[address]
	if !ok {
		name = "Unknown"
	}
	return fmt.Sprintf("%s (%d)", name, address)
}
