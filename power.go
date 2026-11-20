package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/RobertMe/gocec"
)

type PowerState struct {
	state     string
	published bool
}

type PowerBridge struct {
	cec      cecSender
	mqtt     mqttClient
	devices  *DeviceRegistry
	haBridge *HomeAssistantBridge

	// publisher publishes the power states in order, see publishQueue.
	publisher *publishQueue

	// The power status is polled every longInterval. After a command or a
	// power related CEC message it's polled every shortInterval, for
	// shortDuration.
	longInterval  time.Duration
	shortInterval time.Duration
	shortDuration time.Duration

	monitors      map[string]*Monitor
	monitorsMutex sync.Mutex

	states      map[string]*PowerState
	statesMutex sync.Mutex
}

func newPowerBridge(cec cecSender, mqtt mqttClient, devices *DeviceRegistry) *PowerBridge {
	return &PowerBridge{
		cec:       cec,
		mqtt:      mqtt,
		devices:   devices,
		publisher: newPublishQueue(mqtt, 64),

		longInterval: 5 * time.Minute,
		// Some TVs keep reporting "on" for a few seconds after accepting
		// <Standby>, so keep polling for a while.
		shortInterval: 2 * time.Second,
		shortDuration: 30 * time.Second,

		monitors: make(map[string]*Monitor),
		states:   make(map[string]*PowerState),
	}
}

// setupPowerBridge creates the bridge and connects it to CEC, MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupPowerBridge(cec cecBus, mqtt mqttClient, devices *DeviceRegistry, haBridge *HomeAssistantBridge) *PowerBridge {
	bridge := newPowerBridge(cec, mqtt, devices)

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)

	if haBridge != nil {
		slog.Info("Enabling Home Assistant configuration for power")
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	cec.RegisterMessageHandler(bridge.handleReportPowerStatus, gocec.OpcodeReportPowerStatus)
	cec.RegisterMessageHandler(bridge.handleDeviceActivity,
		gocec.OpcodeSetSystemAudioMode,
		gocec.OpcodeReportPhysicalAddress,
		gocec.OpcodeSetMenuLanguage,
		gocec.OpcodeVendorCommand,
		gocec.OpcodeVendorCommandWithId,
	)
	cec.RegisterMessageHandler(bridge.handleBusChange, gocec.OpcodeStandby, gocec.OpcodeActiveSource)

	mqtt.RegisterConnectedHandler(bridge.resendAll)

	return bridge
}

func (bridge *PowerBridge) deviceAdded(device *Device) {
	bridge.statesMutex.Lock()
	bridge.states[device.Id] = &PowerState{state: "unknown", published: false}
	bridge.statesMutex.Unlock()

	request := bridge.createStatusRequester(device)
	monitor := CreateMonitor(
		request,
		bridge.createRunner(device, request),
		bridge.longInterval,
		bridge.shortInterval,
		bridge.shortDuration,
	)

	bridge.monitorsMutex.Lock()
	bridge.monitors[device.Id] = monitor
	bridge.monitorsMutex.Unlock()

	slog.Debug("Subscribing to power change requests", "device.id", device.Id)
	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "power/set"), 0, func(payload []byte) {
		bridge.handlePowerRequest(device, payload)
	})

	if bridge.haBridge != nil {
		bridge.haBridge.RegisterSwitch(device, "power")
	}
}

func (bridge *PowerBridge) handlePowerRequest(device *Device, payload []byte) {
	switch string(payload) {
	case "on":
		slog.Info("Powering device on as requested on MQTT", "device.id", device.Id)
		bridge.cec.PowerOn(device.LogicalAddress)
	case "off":
		slog.Info("Turning device into standby as requested on MQTT", "device.id", device.Id)
		bridge.cec.Standby(device.LogicalAddress)
	default:
		slog.Warn("Unsupported power request, use \"on\" or \"off\"", "device.id", device.Id, "payload", string(payload))
		return
	}

	// Poll the power status more often, so the new state is published soon.
	bridge.MonitorPower(device.Id)
}

func (bridge *PowerBridge) handleReportPowerStatus(message gocec.Message) {
	device := bridge.devices.FindByLogicalAddress(message.Source())
	parameters := message.Parameters()
	if device == nil || len(parameters) < 1 {
		return
	}
	status := gocec.PowerStatus(parameters[0])

	slog.Debug("New power status received",
		"device.id", device.Id,
		"status", status,
	)

	bridge.setPowerStatus(device, status)
}

// handleDeviceActivity checks the power status of the sender soon, instead of
// on the next regular poll. Devices which wake up announce themselves (like a
// TV turned on with its own remote broadcasting its physical address and menu
// language), vendor commands are often sent on power changes as well, and an
// audio system changes its system audio mode when powering on or off.
func (bridge *PowerBridge) handleDeviceActivity(message gocec.Message) {
	device := bridge.devices.FindByLogicalAddress(message.Source())
	if device == nil {
		return
	}

	slog.Debug("Restarting power monitor because of device activity",
		"device.id", device.Id,
		"opcode", message.Opcode(),
	)

	bridge.MonitorPower(device.Id)
}

// handleBusChange restarts the power monitor of all devices, as a broadcast
// standby or a new active source can change the power of any device.
func (bridge *PowerBridge) handleBusChange(message gocec.Message) {
	slog.Debug("Restarting power monitor on all devices",
		"message.source", message.Source(),
		"message.destination", message.Destination(),
		"message.opcode", message.Opcode(),
		"message.raw", []byte(message),
	)

	bridge.monitorsMutex.Lock()
	defer bridge.monitorsMutex.Unlock()
	for _, monitor := range bridge.monitors {
		monitor.Reset()
	}
}

// Close stops polling and publishing.
func (bridge *PowerBridge) Close() {
	bridge.monitorsMutex.Lock()
	monitors := make([]*Monitor, 0, len(bridge.monitors))
	for _, monitor := range bridge.monitors {
		monitors = append(monitors, monitor)
	}
	bridge.monitorsMutex.Unlock()

	for _, monitor := range monitors {
		monitor.Stop()
	}
	bridge.publisher.Close()
}

func (bridge *PowerBridge) MonitorPower(deviceId string) {
	bridge.monitorsMutex.Lock()
	defer bridge.monitorsMutex.Unlock()

	if monitor, ok := bridge.monitors[deviceId]; ok {
		monitor.Reset()
	}
}

func (bridge *PowerBridge) setPowerStatus(device *Device, status gocec.PowerStatus) {
	value := powerStateValue(status)

	bridge.statesMutex.Lock()
	defer bridge.statesMutex.Unlock()
	state, ok := bridge.states[device.Id]
	if !ok {
		return
	}
	if state.state == value && state.published {
		return
	}

	slog.Info("Updating power state",
		"device.logical_address", device.LogicalAddress,
		"device.id", device.Id,
		"state.cec", status,
		"state.converted", value,
	)

	state.state = value

	if value != "unknown" || !state.published {
		// Queued while holding statesMutex, so the states are published in
		// the order they were set.
		bridge.publisher.Publish(bridge.mqtt.BuildTopic(device, "power"), 0, false, value)
		state.published = true
	} else {
		state.published = false
	}
}

// powerStateValue converts a CEC power status into the MQTT state. Transitions
// are reported as the state the device is going to, so the state follows a
// command right away instead of only once the transition has finished.
func powerStateValue(status gocec.PowerStatus) string {
	switch status {
	case gocec.PowerStatusOn, gocec.PowerStatusTransitionToOn:
		return "on"
	case gocec.PowerStatusStandBy, gocec.PowerStatusTransitionToStandby:
		return "off"
	default:
		return "unknown"
	}
}

// powerStatusRequest creates a Give Device Power Status message from the
// adapter to the device. It must be sent from the adapter's own logical
// address: the Linux kernel CEC API rejects messages with another initiator
// (EINVAL), and the device sends its reply to the initiator.
func powerStatusRequest(adapter gocec.LogicalAddress, device *Device) gocec.Message {
	return gocec.NewMessage(adapter, device.LogicalAddress, gocec.OpcodeGiveDevicePowerStatus, []byte{})
}

// createStatusRequester creates a function which asks the device for its power
// status directly. The reply is handled like any other Report Power Status.
// This is needed because libcec's power status (used by the runner) is cached
// for up to 30 seconds.
func (bridge *PowerBridge) createStatusRequester(device *Device) func() {
	var lastSend time.Time

	return func() {
		// The monitor calls this both on (re)start and on every tick, so
		// requests shortly after each other are merged.
		now := time.Now()
		if now.Sub(lastSend) < time.Second {
			return
		}
		lastSend = now

		message := powerStatusRequest(bridge.cec.AdapterAddress(), device)
		logTrace("Requesting power state from monitor",
			"device.logical_address", device.LogicalAddress,
			"device.id", device.Id,
			"message", []byte(message),
		)
		bridge.cec.Transmit(message)
	}
}

func (bridge *PowerBridge) createRunner(device *Device, request func()) Runner {
	return func() {
		request()

		// libcec's (possibly cached) status, for devices which don't reply to
		// the request above.
		status := bridge.cec.PowerStatus(device.LogicalAddress)

		logTrace("Updating power from monitor",
			"device.logical_address", device.LogicalAddress,
			"device.id", device.Id,
			"status", status,
		)

		bridge.setPowerStatus(device, status)
	}
}

func (bridge *PowerBridge) resendAll() {
	slog.Debug("Resending all power states")

	devices := bridge.devices.List()
	if bridge.haBridge != nil {
		for _, device := range devices {
			bridge.statesMutex.Lock()
			_, ok := bridge.states[device.Id]
			bridge.statesMutex.Unlock()
			if ok {
				bridge.haBridge.RegisterSwitch(device, "power")
			}
		}
	}

	// Through the publisher while holding statesMutex, so a state set at the
	// same time can't be overtaken by an older one.
	bridge.statesMutex.Lock()
	defer bridge.statesMutex.Unlock()
	for _, device := range devices {
		if state, ok := bridge.states[device.Id]; ok {
			bridge.publisher.Publish(bridge.mqtt.BuildTopic(device, "power"), 0, false, state.state)
		}
	}
}
