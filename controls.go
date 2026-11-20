package main

import (
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/RobertMe/gocec"
)

// bridgeTopic is the MQTT topic name of the Cec2Mqtt bridge itself, like
// cec2mqtt/bridge/rescan/set. A CEC device must not use it as mqtt_topic.
const bridgeTopic = "bridge"

// BridgeControls publishes the Cec2Mqtt bridge itself, with commands for the
// whole CEC bus (<base>/bridge/standby_all/set and <base>/bridge/rescan/set)
// and diagnostics. In Home Assistant it's a device with buttons, which the CEC
// devices are shown as connected through.
type BridgeControls struct {
	cec      cecBus
	mqtt     mqttClient
	haBridge *HomeAssistantBridge
	device   *Device

	// standbyHook is called after the standby broadcast, so the power states
	// are updated soon.
	standbyHook func()

	scanning atomic.Bool
	scans    sync.WaitGroup
}

var unsafeIdCharacters = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// newBridgeDevice creates the device which represents Cec2Mqtt. Its ID
// contains the base topic, so more than one instance can be connected to Home
// Assistant.
func newBridgeDevice(config *Config) *Device {
	return &Device{
		Id: "bridge_" + unsafeIdCharacters.ReplaceAllString(config.Mqtt.BaseTopic, "_"),
		Config: &DeviceConfig{
			OSD:       "Cec2Mqtt",
			MqttTopic: bridgeTopic,
		},
	}
}

// setupBridgeControls creates the bridge device and subscribes to its
// commands. haBridge is nil when the Home Assistant integration is disabled.
func setupBridgeControls(config *Config, cec cecBus, mqtt mqttClient, haBridge *HomeAssistantBridge, standbyHook func()) *BridgeControls {
	controls := &BridgeControls{
		cec:         cec,
		mqtt:        mqtt,
		haBridge:    haBridge,
		device:      newBridgeDevice(config),
		standbyHook: standbyHook,
	}

	mqtt.Subscribe(mqtt.BuildTopic(controls.device, "standby_all/set"), 0, func([]byte) {
		controls.StandbyAll()
	})
	mqtt.Subscribe(mqtt.BuildTopic(controls.device, "rescan/set"), 0, func([]byte) {
		controls.Rescan()
	})

	if haBridge != nil {
		haBridge.SetBridgeDevice(controls.device)
		haBridge.RegisterBirthHandler(controls.resendAll)
	}
	mqtt.RegisterConnectedHandler(controls.resendAll)

	controls.resendAll()

	return controls
}

// StandbyAll broadcasts <Standby>, which turns off every device on the bus.
func (controls *BridgeControls) StandbyAll() {
	slog.Info("Turning all devices into standby as requested on MQTT")
	controls.cec.Transmit(gocec.NewMessage(controls.cec.AdapterAddress(), gocec.DeviceBroadcast, gocec.OpcodeStandby, []byte{}))

	if controls.standbyHook != nil {
		controls.standbyHook()
	}
}

// Rescan looks for devices which weren't found yet, like devices which were
// off on startup and never sent a message since. It runs in the background, as
// polling all addresses takes a few seconds, and only once at a time.
func (controls *BridgeControls) Rescan() {
	if !controls.scanning.CompareAndSwap(false, true) {
		slog.Info("Already scanning for devices")
		return
	}

	slog.Info("Scanning for devices as requested on MQTT")
	controls.scans.Add(1)
	go func() {
		defer controls.scans.Done()
		defer controls.scanning.Store(false)
		controls.cec.Scan()
	}()
}

// Close waits until a running scan finished.
func (controls *BridgeControls) Close() {
	controls.scans.Wait()
}

func (controls *BridgeControls) resendAll() {
	if controls.haBridge != nil {
		controls.haBridge.RegisterButton(controls.device, "standby_all", "standby_all", "press", "mdi:power-sleep")
		controls.haBridge.RegisterButton(controls.device, "rescan", "rescan", "press", "mdi:magnify-scan")
		controls.haBridge.RegisterDiagnosticSensor(controls.device, "version", "mdi:tag")
		controls.haBridge.RegisterDiagnosticSensor(controls.device, "adapter", "mdi:hdmi-port")
	}

	controls.mqtt.Publish(controls.mqtt.BuildTopic(controls.device, "version"), 0, false, BuildVersion)
	controls.mqtt.Publish(controls.mqtt.BuildTopic(controls.device, "adapter"), 0, false, controls.cec.AdapterPath())
}
