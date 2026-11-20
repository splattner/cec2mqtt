package bridge

import (
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
	"github.com/splattner/cec2mqtt/internal/logging"
)

type ActiveSource struct {
	// mux guards activeSource and allowedSources, which are accessed from both
	// libcec callbacks and the monitor goroutine.
	mux sync.Mutex

	sender         cecSender
	mqtt           MQTT
	activeSource   *device.Device
	devices        *device.Registry
	monitor        *Monitor
	allowedSources map[gocec.LogicalAddress]bool
	haBridge       *homeassistant.Discovery

	// tv is the TV, which has the "source" select listing all sources.
	tv *device.Device
	// sources holds all (not ignored) devices by their MQTT topic name.
	sources map[string]*device.Device
}

func newActiveSource(sender cecSender, mqtt MQTT, devices *device.Registry) *ActiveSource {
	return &ActiveSource{
		sender:         sender,
		mqtt:           mqtt,
		devices:        devices,
		allowedSources: make(map[gocec.LogicalAddress]bool),
		sources:        make(map[string]*device.Device),
	}
}

// setupActiveSource creates the bridge and connects it to CEC, MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupActiveSource(cec CEC, mqtt MQTT, devices *device.Registry, haBridge *homeassistant.Discovery) *ActiveSource {

	bridge := newActiveSource(cec, mqtt, devices)

	if haBridge != nil {
		slog.Info("Enabling Home Assistant configuration for active source")
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendAll)
	}

	bridge.monitor = CreateMonitor(
		func() {},
		bridge.checkActiveSource,
		10*time.Minute,
		10*time.Second,
		1*time.Minute,
	)

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)

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

		parameters := message.Parameters()
		if len(parameters) < 1 {
			return
		}

		powerStatus := gocec.PowerStatus(parameters[0])
		allowed := powerStatus != gocec.PowerStatusStandBy

		bridge.mux.Lock()
		defer bridge.mux.Unlock()

		// Only check the active source more often when the power status changed.
		// Power status is polled regularly, and requesting the active source
		// while nothing is active causes a lot of (unanswered) CEC traffic.
		if previous, ok := bridge.allowedSources[message.Source()]; !ok || previous != allowed {
			defer bridge.monitor.Reset()
		}
		bridge.allowedSources[message.Source()] = allowed

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

	return bridge
}

func (bridge *ActiveSource) updateActiveSource(newSource *device.Device) {
	bridge.mux.Lock()
	defer bridge.mux.Unlock()
	bridge.updateActiveSourceLocked(newSource)
}

// updateActiveSourceLocked must be called while holding bridge.mux.
func (bridge *ActiveSource) updateActiveSourceLocked(newSource *device.Device) {
	if bridge.activeSource != nil && newSource == bridge.activeSource {
		return
	}

	if newSource == nil && bridge.activeSource == nil {
		return
	}

	if newSource != nil {
		if allowed, ok := bridge.allowedSources[newSource.LogicalAddress]; ok && !allowed {
			if bridge.activeSource == nil {
				logging.Trace("Skipping active source update because active device still is in standby",
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
	bridge.publishSourceLocked(newSource)

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

// Close stops checking the active source.
func (bridge *ActiveSource) Close() {
	if bridge.monitor != nil {
		bridge.monitor.Stop()
	}
}

func (bridge *ActiveSource) checkActiveSource() {
	// Nothing can be the active source while the TV is in standby. Asking
	// anyway makes libcec broadcast Request Active Source, which nobody answers.
	bridge.mux.Lock()
	tvOn, known := bridge.allowedSources[gocec.DeviceTV]
	bridge.mux.Unlock()
	if known && !tvOn {
		logging.Trace("Not checking active source because the TV is in standby")
		bridge.updateActiveSource(nil)
		return
	}

	address := bridge.sender.ActiveSource()

	var newSource *device.Device = nil
	newSourceId := ""
	if address != gocec.DeviceUnknown {
		if newSource = bridge.devices.FindByLogicalAddress(address); newSource != nil {
			newSourceId = newSource.Id
		}
	}

	logging.Trace("Updating active source from monitor",
		"active_source.logical_address", address,
		"active_source.id", newSourceId,
	)

	bridge.updateActiveSource(newSource)
}

func (bridge *ActiveSource) resendAll() {
	slog.Debug("Resending all active source states")
	bridge.mux.Lock()
	activeSource := bridge.activeSource
	bridge.mux.Unlock()

	bridge.mux.Lock()
	bridge.registerSourceSelectLocked()
	bridge.publishSourceLocked(activeSource)
	sources := make([]*device.Device, 0, len(bridge.sources))
	for _, device := range bridge.sources {
		sources = append(sources, device)
	}
	bridge.mux.Unlock()

	for _, device := range sources {
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

func (bridge *ActiveSource) deviceAdded(device *device.Device) {
	bridge.mux.Lock()
	bridge.allowedSources[device.LogicalAddress] = true
	bridge.sources[device.Config.MqttTopic] = device
	if device.LogicalAddress == gocec.DeviceTV {
		bridge.tv = device
	}
	bridge.registerSourceSelectLocked()
	bridge.mux.Unlock()

	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "is_active_source/set"), 0, func(payload []byte) {
		if string(payload) != "on" {
			slog.Debug("Ignoring active source request, only \"on\" is supported",
				"device.id", device.Id,
				"payload", string(payload),
			)
			return
		}

		slog.Info("Selecting device as active source as requested on MQTT", "device.id", device.Id)
		bridge.selectSource(device)
	})

	if device.LogicalAddress == gocec.DeviceTV {
		bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "source/set"), 0, bridge.handleSourceRequest)
	}

	if bridge.haBridge != nil {
		bridge.haBridge.RegisterBinarySensor(device, "is_active_source")
	}
}

// handleSourceRequest handles <TV>/source/set, which contains the MQTT topic
// name of the device to switch to.
func (bridge *ActiveSource) handleSourceRequest(payload []byte) {
	name := string(payload)

	bridge.mux.Lock()
	device, ok := bridge.sources[name]
	bridge.mux.Unlock()

	if !ok {
		slog.Warn("Unknown source requested on MQTT", "source", name)
		return
	}

	slog.Info("Selecting source as requested on MQTT", "source", name, "device.id", device.Id)
	bridge.selectSource(device)
}

func (bridge *ActiveSource) selectSource(device *device.Device) {
	selectSource(bridge.sender, device)
	if bridge.monitor != nil {
		bridge.monitor.Reset()
	}
}

// sourceNamesLocked returns the sorted MQTT topic names of all sources.
func (bridge *ActiveSource) sourceNamesLocked() []string {
	names := make([]string, 0, len(bridge.sources))
	for name := range bridge.sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// registerSourceSelectLocked (re)registers the "source" select of the TV, as
// the options change with every device found. Must hold bridge.mux.
func (bridge *ActiveSource) registerSourceSelectLocked() {
	if bridge.haBridge == nil || bridge.tv == nil {
		return
	}
	bridge.haBridge.RegisterSelect(bridge.tv, "source", bridge.sourceNamesLocked())
}

// publishSourceLocked publishes the active source on <TV>/source. Must hold bridge.mux.
func (bridge *ActiveSource) publishSourceLocked(source *device.Device) {
	if bridge.tv == nil {
		return
	}

	value := "None" // resets the select to unknown in Home Assistant
	if source != nil {
		value = source.Config.MqttTopic
	}
	bridge.mqtt.Publish(bridge.mqtt.BuildTopic(bridge.tv, "source"), 0, false, value)
}

// selectSource makes the device the active source using One Touch Play: the TV
// is woken up (Image View On) and asked to switch to the physical address of
// the device (Set Stream Path). The device itself then announces it is the
// active source.
func selectSource(cec cecSender, device *device.Device) {
	source := cec.AdapterAddress()
	address := device.CecDevice.PhysicalAddress

	cec.Transmit(gocec.NewMessage(source, gocec.DeviceTV, gocec.OpcodeImageViewOn, []byte{}))
	cec.Transmit(gocec.NewMessage(source, gocec.DeviceBroadcast, gocec.OpcodeSetStreamPath, []byte{address[0], address[1]}))
}
