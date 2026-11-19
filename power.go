package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/RobertMe/gocec"
)

func init() {
	RegisterInitializer(0, InitPowerBridge)
}

type PowerState struct {
	state     string
	published bool
}

type PowerBridge struct {
	cec      *Cec
	mqtt     *Mqtt
	devices  *DeviceRegistry
	haBridge *HomeAssistantBridge

	monitors      map[string]*Monitor
	monitorsMutex sync.Mutex

	states      map[string]*PowerState
	statesMutex sync.Mutex
}

func InitPowerBridge(container *Container) {
	cec := container.Get("cec").(*Cec)
	mqtt := container.Get("mqtt").(*Mqtt)
	devices := container.Get("devices").(*DeviceRegistry)
	bridge := &PowerBridge{
		cec:     cec,
		mqtt:    mqtt,
		devices: devices,

		monitors: make(map[string]*Monitor),
		states:   make(map[string]*PowerState),
	}

	container.Register("bridge.power", bridge)

	devices.RegisterDeviceAddedHandler(func(device *Device) {
		bridge.statesMutex.Lock()
		bridge.monitorsMutex.Lock()
		defer bridge.statesMutex.Unlock()
		defer bridge.monitorsMutex.Unlock()
		bridge.states[device.Id] = &PowerState{state: "unknown", published: false}
		bridge.monitors[device.Id] = CreateMonitor(
			bridge.createStarter(device),
			bridge.createRunner(device),
			5*time.Minute,
			5*time.Second,
			time.Minute,
		)

		slog.Debug("Subscribing to power change requests", "device.id", device.Id)

		mqtt.Subscribe(mqtt.BuildTopic(device, "power/set"), 0, func(payload []byte) {
			switch string(payload) {
			case "on":
				slog.Info("Powering device on as requested on MQTT", "device.id", device.Id)
				cec.connection.PowerOnDevice(device.LogicalAddress)
			case "off":
				slog.Info("Turning device into standby as requested on MQTT", "device.id", device.Id)
				cec.connection.StandByDevice(device.LogicalAddress)
			default:
				slog.Warn("Unsupported power request, use \"on\" or \"off\"", "device.id", device.Id, "payload", string(payload))
				return
			}

			// Poll the power status more often, so the new state is published soon.
			bridge.MonitorPower(device.Id)
		})
	})

	if haBridge, ok := container.Get("home-assistant").(*HomeAssistantBridge); ok {
		slog.Info("Enabling Home Assistant configuration for power")
		bridge.haBridge = haBridge
		devices.RegisterDeviceAddedHandler(func(device *Device) {
			haBridge.RegisterSwitch(device, "power")
		})
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	getDevice := func(address gocec.LogicalAddress) *Device {
		return devices.FindByLogicalAddress(address)
	}

	cec.RegisterMessageHandler(func(message gocec.Message) {
		device := getDevice(message.Source())
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
	}, gocec.OpcodeReportPowerStatus)

	cec.RegisterMessageHandler(func(message gocec.Message) {
		device := getDevice(message.Source())
		if device == nil {
			return
		}

		slog.Debug("Restarting power monitor because of system audio mode change", "device.id", device.Id)

		bridge.MonitorPower(device.Id)
	}, gocec.OpcodeSetSystemAudioMode)

	cec.RegisterMessageHandler(func(message gocec.Message) {
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
	}, gocec.OpcodeStandby, gocec.OpcodeActiveSource)

	mqtt.RegisterConnectedHandler(bridge.resendAll)
}

func (bridge *PowerBridge) MonitorPower(deviceId string) {
	bridge.monitorsMutex.Lock()
	defer bridge.monitorsMutex.Unlock()

	if monitor, ok := bridge.monitors[deviceId]; ok {
		monitor.Reset()
	}
}

func (bridge *PowerBridge) setPowerStatus(device *Device, status gocec.PowerStatus) {
	var value string
	switch status {
	case gocec.PowerStatusOn, gocec.PowerStatusTransitionToStandby:
		value = "on"
	case gocec.PowerStatusStandBy, gocec.PowerStatusTransitionToOn:
		value = "off"
	default:
		value = "unknown"
	}

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
		go bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "power"), 0, false, value)
		state.published = true
	} else {
		state.published = false
	}
}

func (bridge *PowerBridge) createStarter(device *Device) Starter {
	source := gocec.DeviceTV

	if device.LogicalAddress == gocec.DeviceTV {
		source = gocec.DeviceBroadcast
	}

	message := gocec.NewMessage(source, device.LogicalAddress, gocec.OpcodeGiveDevicePowerStatus, []byte{})

	context := slog.With(
		"device.logical_address", device.LogicalAddress,
		"device.id", device.Id,
		"source", source,
		"message", []byte(message),
	)

	var lastSend time.Time

	return func() {
		now := time.Now()
		if now.Sub(lastSend) > 10*time.Second {
			trace(context, "Requesting power state from monitor")
			bridge.cec.Transmit(message)
			lastSend = now
		}
	}
}

func (bridge *PowerBridge) createRunner(device *Device) Runner {
	return func() {
		status := bridge.cec.connection.GetPowerStatus(device.LogicalAddress)

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
	bridge.statesMutex.Lock()
	defer bridge.statesMutex.Unlock()
	for _, device := range bridge.devices.List() {
		var state *PowerState
		var ok bool
		if state, ok = bridge.states[device.Id]; !ok {
			continue
		}

		if bridge.haBridge != nil {
			bridge.haBridge.RegisterSwitch(device, "power")
		}

		bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "power"), 0, false, state.state)
	}
}
