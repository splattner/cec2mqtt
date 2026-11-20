package bridge

import (
	"log/slog"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/splattner/cec2mqtt/internal/config"
	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
	"github.com/splattner/cec2mqtt/internal/version"
)

// Controls publishes the Cec2Mqtt bridge itself, with commands for the
// whole CEC bus (<base>/bridge/standby_all/set and <base>/bridge/rescan/set)
// and diagnostics. In Home Assistant it's a device with buttons, which the CEC
// devices are shown as connected through.
type Controls struct {
	cec      CEC
	mqtt     MQTT
	haBridge *homeassistant.Discovery
	device   *device.Device

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
func newBridgeDevice(config *config.Config) *device.Device {
	return &device.Device{
		Id: "bridge_" + unsafeIdCharacters.ReplaceAllString(config.Mqtt.BaseTopic, "_"),
		Config: &device.Config{
			OSD:       "Cec2Mqtt",
			MqttTopic: device.ReservedTopic,
		},
	}
}

// setupControls creates the bridge device and subscribes to its
// commands. haBridge is nil when the Home Assistant integration is disabled.
func setupControls(config *config.Config, cec CEC, mqtt MQTT, haBridge *homeassistant.Discovery, standbyHook func()) *Controls {
	controls := &Controls{
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
func (controls *Controls) StandbyAll() {
	slog.Info("Turning all devices into standby as requested on MQTT")
	controls.cec.Transmit(gocec.NewMessage(controls.cec.AdapterAddress(), gocec.DeviceBroadcast, gocec.OpcodeStandby, []byte{}))

	if controls.standbyHook != nil {
		controls.standbyHook()
	}
}

// Rescan looks for devices which weren't found yet, like devices which were
// off on startup and never sent a message since. It runs in the background, as
// polling all addresses takes a few seconds, and only once at a time.
func (controls *Controls) Rescan() {
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
func (controls *Controls) Close() {
	controls.scans.Wait()
}

func (controls *Controls) resendAll() {
	if controls.haBridge != nil {
		controls.haBridge.RegisterButton(controls.device, "standby_all", "standby_all", "press", "mdi:power-sleep")
		controls.haBridge.RegisterButton(controls.device, "rescan", "rescan", "press", "mdi:magnify-scan")
		controls.haBridge.RegisterDiagnosticSensor(controls.device, "version", "mdi:tag")
		controls.haBridge.RegisterDiagnosticSensor(controls.device, "adapter", "mdi:hdmi-port")
	}

	controls.mqtt.Publish(controls.mqtt.BuildTopic(controls.device, "version"), 0, false, version.Version)
	controls.mqtt.Publish(controls.mqtt.BuildTopic(controls.device, "adapter"), 0, false, controls.cec.AdapterPath())
}
