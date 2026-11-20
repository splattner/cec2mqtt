package bridge

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
)

// Remote sends key presses to devices (<device>/key/set) and publishes
// key presses received from devices, like the TV forwarding its remote control
// buttons (<device>/remote_key).
type Remote struct {
	cec      cecSender
	mqtt     MQTT
	devices  *device.Registry
	haBridge *homeassistant.Discovery

	mux sync.Mutex
	// eventEntities holds the devices which have a Home Assistant event entity.
	eventEntities map[string]*device.Device
}

func newRemote(cec cecSender, mqtt MQTT, devices *device.Registry) *Remote {
	return &Remote{
		cec:           cec,
		mqtt:          mqtt,
		devices:       devices,
		eventEntities: make(map[string]*device.Device),
	}
}

// setupRemote creates the bridge and connects it to CEC, MQTT and the
// device registry. haBridge is nil when the Home Assistant integration is disabled.
func setupRemote(cec CEC, mqtt MQTT, devices *device.Registry, haBridge *homeassistant.Discovery) *Remote {
	bridge := newRemote(cec, mqtt, devices)

	if haBridge != nil {
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendDiscovery)
		mqtt.RegisterConnectedHandler(bridge.resendDiscovery)
	}

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)
	cec.RegisterMessageHandler(bridge.handleUserControlPressed, gocec.OpcodeUserControlPressed)

	return bridge
}

func (bridge *Remote) deviceAdded(device *device.Device) {
	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "key/set"), 0, func(payload []byte) {
		bridge.handleKeyRequest(device, payload)
	})

	// The TV is the device which forwards its remote control, so register its
	// event entity right away. Others are registered on their first key press.
	if device.LogicalAddress == gocec.DeviceTV {
		bridge.ensureEventEntity(device)
	}
}

func (bridge *Remote) handleKeyRequest(device *device.Device, payload []byte) {
	name := strings.ToLower(strings.TrimSpace(string(payload)))
	code, ok := KeyCode(name)
	if !ok {
		slog.Warn("Unknown key requested on MQTT",
			"device.id", device.Id,
			"key", name,
		)
		return
	}

	slog.Info("Sending key as requested on MQTT",
		"device.id", device.Id,
		"key", name,
	)
	sendKey(bridge.cec, device.LogicalAddress, code)
}

func (bridge *Remote) handleUserControlPressed(message gocec.Message) {
	parameters := message.Parameters()
	if len(parameters) < 1 {
		return
	}

	device := bridge.devices.FindByLogicalAddress(message.Source())
	if device == nil {
		return
	}

	key := KeyName(parameters[0])
	slog.Debug("Key press received",
		"device.id", device.Id,
		"destination", message.Destination(),
		"key", key,
	)

	bridge.ensureEventEntity(device)

	payload, _ := json.Marshal(map[string]string{"event_type": key})
	bridge.mqtt.Publish(bridge.mqtt.BuildTopic(device, "remote_key"), 0, false, payload)
}

func (bridge *Remote) ensureEventEntity(device *device.Device) {
	if bridge.haBridge == nil {
		return
	}

	bridge.mux.Lock()
	_, registered := bridge.eventEntities[device.Id]
	bridge.eventEntities[device.Id] = device
	bridge.mux.Unlock()

	if !registered {
		bridge.haBridge.RegisterEvent(device, "remote_key", KeyNames())
	}
}

func (bridge *Remote) resendDiscovery() {
	bridge.mux.Lock()
	devices := make([]*device.Device, 0, len(bridge.eventEntities))
	for _, device := range bridge.eventEntities {
		devices = append(devices, device)
	}
	bridge.mux.Unlock()

	for _, device := range devices {
		bridge.haBridge.RegisterEvent(device, "remote_key", KeyNames())
	}
}
