package main

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

// diagnosticProperties are the diagnostic values published per device, in
// <device>/<property>, and their Home Assistant icons.
var diagnosticProperties = []struct{ property, icon string }{
	{"physical_address", "mdi:hdmi-port"},
	{"logical_address", "mdi:identifier"},
	{"vendor", "mdi:factory"},
}

// DiagnosticsBridge publishes how each device is connected to the CEC bus. In
// Home Assistant these are diagnostic sensors, which are hidden from the
// default dashboards but help to find out which device is which.
type DiagnosticsBridge struct {
	mqtt     mqttClient
	haBridge *HomeAssistantBridge

	mux     sync.Mutex
	devices map[string]*Device
}

func newDiagnosticsBridge(mqtt mqttClient) *DiagnosticsBridge {
	return &DiagnosticsBridge{
		mqtt:    mqtt,
		devices: make(map[string]*Device),
	}
}

// setupDiagnosticsBridge creates the bridge and connects it to MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupDiagnosticsBridge(mqtt mqttClient, devices *DeviceRegistry, haBridge *HomeAssistantBridge) *DiagnosticsBridge {
	bridge := newDiagnosticsBridge(mqtt)

	if haBridge != nil {
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)
	mqtt.RegisterConnectedHandler(bridge.resendAll)

	return bridge
}

func (bridge *DiagnosticsBridge) deviceAdded(device *Device) {
	bridge.mux.Lock()
	bridge.devices[device.Id] = device
	bridge.mux.Unlock()

	bridge.registerEntities(device)
	bridge.publish(device)
}

func (bridge *DiagnosticsBridge) registerEntities(device *Device) {
	if bridge.haBridge == nil {
		return
	}

	for _, diagnostic := range diagnosticProperties {
		bridge.haBridge.RegisterDiagnosticSensor(device, diagnostic.property, diagnostic.icon)
	}
}

func (bridge *DiagnosticsBridge) publish(device *Device) {
	values := map[string]string{
		"physical_address": device.CecDevice.physicalAddress.String(),
		"logical_address":  logicalAddressName(device.LogicalAddress),
		"vendor":           device.CecDevice.vendor.String(),
	}

	slog.Debug("Publishing diagnostics", "device.id", device.Id, "values", values)

	for _, diagnostic := range diagnosticProperties {
		bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, diagnostic.property), 0, false, values[diagnostic.property])
	}
}

func (bridge *DiagnosticsBridge) resendAll() {
	bridge.mux.Lock()
	devices := make([]*Device, 0, len(bridge.devices))
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
