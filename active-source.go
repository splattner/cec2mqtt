package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/RobertMe/gocec"
)

func init() {
	RegisterInitializer(0, InitActiveSourceBridge)
}

type ActiveSourceBridge struct {
	// mux guards activeSource and allowedSources, which are accessed from both
	// libcec callbacks and the monitor goroutine.
	mux sync.Mutex

	cec            *Cec
	mqtt           *Mqtt
	activeSource   *Device
	devices        *DeviceRegistry
	monitor        *Monitor
	allowedSources map[gocec.LogicalAddress]bool
	haBridge       *HomeAssistantBridge
}

func InitActiveSourceBridge(container *Container) {
	cec := container.Get("cec").(*Cec)
	mqtt := container.Get("mqtt").(*Mqtt)
	devices := container.Get("devices").(*DeviceRegistry)

	bridge := &ActiveSourceBridge{
		cec:            cec,
		mqtt:           mqtt,
		activeSource:   nil,
		devices:        devices,
		allowedSources: make(map[gocec.LogicalAddress]bool),
	}

	bridge.monitor = CreateMonitor(
		func() {},
		bridge.checkActiveSource,
		10*time.Minute,
		10*time.Second,
		1*time.Minute,
	)

	devices.RegisterDeviceAddedHandler(func(device *Device) {
		bridge.mux.Lock()
		defer bridge.mux.Unlock()
		bridge.allowedSources[device.LogicalAddress] = true
	})

	if haBridge, ok := container.Get("home-assistant").(*HomeAssistantBridge); ok {
		slog.Info("Enabling Home Assistant configuration for active source")
		bridge.haBridge = haBridge
		devices.RegisterDeviceAddedHandler(func(device *Device) {
			haBridge.RegisterBinarySensor(device, "is_active_source")
		})
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	cec.RegisterMessageHandler(func(message gocec.Message) {
		slog.Debug("Restarting active source monitor",
			"message.source", message.Source(),
			"message.destination", message.Destination(),
			"message.opcode", message.Opcode(),
			"message.raw", []byte(message),
		)

		bridge.monitor.Reset()
	}, gocec.OpcodeActiveSource, gocec.OpcodeSetStreamPath)

	cec.RegisterMessageHandler(func(message gocec.Message) {
		slog.Debug("Updating active source based on power status",
			"message.source", message.Source(),
			"message.destination", message.Destination(),
			"message.opcode", message.Opcode(),
			"message.raw", []byte(message),
		)

		defer bridge.monitor.Reset()

		parameters := message.Parameters()
		if len(parameters) < 1 {
			return
		}

		powerStatus := gocec.PowerStatus(parameters[0])

		bridge.mux.Lock()
		defer bridge.mux.Unlock()
		bridge.allowedSources[message.Source()] = powerStatus != gocec.PowerStatusStandBy

		if bridge.activeSource == nil || message.Source() != bridge.activeSource.LogicalAddress {
			return
		}

		if powerStatus == gocec.PowerStatusStandBy {
			bridge.updateActiveSourceLocked(nil)
		}
	}, gocec.OpcodeReportPowerStatus)

	cec.RegisterMessageHandler(func(message gocec.Message) {
		if message.Source() == gocec.DeviceTV {
			slog.Debug("Setting active source to nil because TV is in standby")
			bridge.updateActiveSource(nil)
		}
	}, gocec.OpcodeStandby)

	mqtt.RegisterConnectedHandler(bridge.resendAll)

	container.Register("active-source", bridge)
}

func (bridge *ActiveSourceBridge) updateActiveSource(newSource *Device) {
	bridge.mux.Lock()
	defer bridge.mux.Unlock()
	bridge.updateActiveSourceLocked(newSource)
}

// updateActiveSourceLocked must be called while holding bridge.mux.
func (bridge *ActiveSourceBridge) updateActiveSourceLocked(newSource *Device) {
	if bridge.activeSource != nil && newSource == bridge.activeSource {
		return
	}

	if newSource == nil && bridge.activeSource == nil {
		return
	}

	if newSource != nil {
		if allowed, ok := bridge.allowedSources[newSource.LogicalAddress]; ok && !allowed {
			if bridge.activeSource == nil {
				logTrace("Skipping active source update because active device still is in standby",
					"device.id", newSource.Id,
					"device.logical_address", newSource.LogicalAddress,
				)

				return
			}

			newSource = nil
		}
	}

	from, to := "", ""
	if bridge.activeSource != nil {
		from = bridge.activeSource.Id
	}
	if newSource != nil {
		to = newSource.Id
	}

	slog.Info("Updating active source",
		"from", from,
		"to", to,
	)

	mqtt := bridge.mqtt
	if bridge.activeSource != nil {
		slog.Debug("Setting device as inactive source", "device.id", bridge.activeSource.Id)

		mqtt.Publish(mqtt.BuildTopic(bridge.activeSource, "is_active_source"), 0, false, "off")
	}

	if newSource != nil {
		slog.Debug("Setting device as active source", "device.id", newSource.Id)

		mqtt.Publish(mqtt.BuildTopic(newSource, "is_active_source"), 0, false, "on")
	}

	bridge.activeSource = newSource
}

func (bridge *ActiveSourceBridge) checkActiveSource() {
	address := bridge.cec.connection.GetActiveSource()

	var newSource *Device = nil
	newSourceId := ""
	if address != gocec.DeviceUnknown {
		if newSource = bridge.devices.FindByLogicalAddress(address); newSource != nil {
			newSourceId = newSource.Id
		}
	}

	logTrace("Updating active source from monitor",
		"active_source.logical_address", address,
		"active_source.id", newSourceId,
	)

	bridge.updateActiveSource(newSource)
}

func (bridge *ActiveSourceBridge) resendAll() {
	slog.Debug("Resending all active source states")
	bridge.mux.Lock()
	activeSource := bridge.activeSource
	bridge.mux.Unlock()

	for _, device := range bridge.devices.List() {
		if bridge.haBridge != nil {
			bridge.haBridge.RegisterBinarySensor(device, "is_active_source")
		}

		value := "off"
		if activeSource != nil && device.Id == activeSource.Id {
			value = "on"
		}

		bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "is_active_source"), 0, false, value)

	}
}
