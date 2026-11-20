package main

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

// audioStatus is the decoded operand of a Report Audio Status message.
type audioStatus struct {
	volume int // 0 - 100
	muted  bool
}

// parseAudioStatus decodes the operand of a Report Audio Status message. Bit 7
// is the mute status and bits 0-6 the volume (0 - 100, 0x7F means unknown).
func parseAudioStatus(operand byte) (status audioStatus, volumeKnown bool) {
	status.muted = operand&0x80 != 0
	status.volume = int(operand & 0x7F)
	return status, status.volume <= 100
}

// AudioBridge exposes the volume and mute status of the audio system (like an
// AV receiver or soundbar) and allows changing them:
//
//	<device>/volume       current volume (0 - 100)
//	<device>/volume/set   "up" or "down"
//	<device>/mute         "on" or "off"
//	<device>/mute/set     "on", "off" or "toggle"
type AudioBridge struct {
	cec      cecSender
	mqtt     mqttClient
	haBridge *HomeAssistantBridge

	monitorIntervals [3]time.Duration

	mux         sync.Mutex
	device      *Device
	monitor     *Monitor
	status      audioStatus
	statusKnown bool
	volumeKnown bool
}

func newAudioBridge(cec cecSender, mqtt mqttClient) *AudioBridge {
	return &AudioBridge{
		cec:  cec,
		mqtt: mqtt,
		// long interval, short interval, short duration
		monitorIntervals: [3]time.Duration{time.Minute, 2 * time.Second, 20 * time.Second},
	}
}

// setupAudioBridge creates the bridge and connects it to CEC, MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupAudioBridge(cec cecBus, mqtt mqttClient, devices *DeviceRegistry, haBridge *HomeAssistantBridge) *AudioBridge {

	bridge := newAudioBridge(cec, mqtt)

	if haBridge != nil {
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)
	cec.RegisterMessageHandler(bridge.handleReportAudioStatus, gocec.OpcodeReportAudioStatus)
	cec.RegisterMessageHandler(func(gocec.Message) {
		bridge.refresh()
	}, gocec.OpcodeSetSystemAudioMode, gocec.OpcodeActiveSource, gocec.OpcodeStandby)
	mqtt.RegisterConnectedHandler(bridge.resendAll)

	return bridge
}

func (bridge *AudioBridge) deviceAdded(device *Device) {
	if device.LogicalAddress != gocec.DeviceAudiosystem {
		return
	}

	slog.Info("Found audio system, enabling volume control", "device.id", device.Id)

	bridge.mux.Lock()
	bridge.device = device
	bridge.mux.Unlock()

	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "volume/set"), 0, func(payload []byte) {
		bridge.handleVolumeRequest(device, payload)
	})
	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "mute/set"), 0, func(payload []byte) {
		bridge.handleMuteRequest(device, payload)
	})

	bridge.registerEntities(device)

	monitor := CreateMonitor(
		func() {},
		func() { bridge.requestStatus(device) },
		bridge.monitorIntervals[0],
		bridge.monitorIntervals[1],
		bridge.monitorIntervals[2],
	)

	bridge.mux.Lock()
	bridge.monitor = monitor
	bridge.mux.Unlock()
}

// Close stops polling the audio status.
func (bridge *AudioBridge) Close() {
	bridge.mux.Lock()
	monitor := bridge.monitor
	bridge.mux.Unlock()

	if monitor != nil {
		monitor.Stop()
	}
}

func (bridge *AudioBridge) registerEntities(device *Device) {
	if bridge.haBridge == nil {
		return
	}

	bridge.haBridge.RegisterSensor(device, "volume", "%")
	bridge.haBridge.RegisterSwitch(device, "mute")
	bridge.haBridge.RegisterButton(device, "volume_up", "volume", "up", "mdi:volume-plus")
	bridge.haBridge.RegisterButton(device, "volume_down", "volume", "down", "mdi:volume-minus")
}

func (bridge *AudioBridge) requestStatus(device *Device) {
	logTrace("Requesting audio status", "device.id", device.Id)
	bridge.cec.Transmit(gocec.NewMessage(bridge.cec.AdapterAddress(), device.LogicalAddress, gocec.OpcodeGiveAudioStatus, []byte{}))
}

// refresh polls the audio status more often for a while, e.g. after it's
// likely to have changed.
func (bridge *AudioBridge) refresh() {
	bridge.mux.Lock()
	monitor := bridge.monitor
	bridge.mux.Unlock()

	if monitor != nil {
		monitor.Reset()
	}
}

func (bridge *AudioBridge) handleVolumeRequest(device *Device, payload []byte) {
	var key string
	switch strings.ToLower(strings.TrimSpace(string(payload))) {
	case "up":
		key = "volume_up"
	case "down":
		key = "volume_down"
	default:
		slog.Warn("Unsupported volume request, use \"up\" or \"down\"",
			"device.id", device.Id,
			"payload", string(payload),
		)
		return
	}

	slog.Info("Changing volume as requested on MQTT", "device.id", device.Id, "key", key)
	code, _ := KeyCode(key)
	sendKey(bridge.cec, device.LogicalAddress, code)
	bridge.refresh()
}

func (bridge *AudioBridge) handleMuteRequest(device *Device, payload []byte) {
	request := strings.ToLower(strings.TrimSpace(string(payload)))

	bridge.mux.Lock()
	known, muted := bridge.statusKnown, bridge.status.muted
	bridge.mux.Unlock()

	switch request {
	case "toggle":
	case "on", "off":
		// CEC only has a mute toggle key, so only press it when the state differs.
		if known && muted == (request == "on") {
			slog.Debug("Audio system already has requested mute state", "device.id", device.Id, "mute", request)
			bridge.publish(device)
			return
		}
	default:
		slog.Warn("Unsupported mute request, use \"on\", \"off\" or \"toggle\"",
			"device.id", device.Id,
			"payload", string(payload),
		)
		return
	}

	slog.Info("Toggling mute as requested on MQTT", "device.id", device.Id, "request", request)
	code, _ := KeyCode("mute")
	sendKey(bridge.cec, device.LogicalAddress, code)
	bridge.refresh()
}

func (bridge *AudioBridge) handleReportAudioStatus(message gocec.Message) {
	parameters := message.Parameters()
	if len(parameters) < 1 {
		return
	}

	bridge.mux.Lock()
	device := bridge.device
	if device == nil || message.Source() != device.LogicalAddress {
		bridge.mux.Unlock()
		return
	}

	status, volumeKnown := parseAudioStatus(parameters[0])
	changed := !bridge.statusKnown || bridge.status != status || bridge.volumeKnown != volumeKnown
	bridge.status = status
	bridge.statusKnown = true
	bridge.volumeKnown = volumeKnown
	bridge.mux.Unlock()

	if !changed {
		return
	}

	slog.Info("Updating audio status",
		"device.id", device.Id,
		"volume", status.volume,
		"volume_known", volumeKnown,
		"muted", status.muted,
	)
	bridge.publish(device)
}

func (bridge *AudioBridge) publish(device *Device) {
	bridge.mux.Lock()
	status, statusKnown, volumeKnown := bridge.status, bridge.statusKnown, bridge.volumeKnown
	bridge.mux.Unlock()

	if !statusKnown {
		return
	}

	mute := "off"
	if status.muted {
		mute = "on"
	}
	bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "mute"), 0, false, mute)

	if volumeKnown {
		bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "volume"), 0, false, strconv.Itoa(status.volume))
	}
}

func (bridge *AudioBridge) resendAll() {
	bridge.mux.Lock()
	device := bridge.device
	bridge.mux.Unlock()

	if device == nil {
		return
	}

	bridge.registerEntities(device)
	bridge.publish(device)
}
