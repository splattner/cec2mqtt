package main

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/RobertMe/gocec"
)

func init() {
	RegisterInitializer(0, InitRemoteBridge)
}

// RemoteBridge sends key presses to devices (<device>/key/set) and publishes
// key presses received from devices, like the TV forwarding its remote control
// buttons (<device>/remote_key).
type RemoteBridge struct {
	cec      cecSender
	mqtt     mqttClient
	devices  *DeviceRegistry
	haBridge *HomeAssistantBridge

	mux sync.Mutex
	// eventEntities holds the devices which have a Home Assistant event entity.
	eventEntities map[string]*Device
}

func newRemoteBridge(cec cecSender, mqtt mqttClient, devices *DeviceRegistry) *RemoteBridge {
	return &RemoteBridge{
		cec:           cec,
		mqtt:          mqtt,
		devices:       devices,
		eventEntities: make(map[string]*Device),
	}
}

func InitRemoteBridge(container *Container) {
	cec := container.Get("cec").(*Cec)
	mqtt := container.Get("mqtt").(*Mqtt)
	devices := container.Get("devices").(*DeviceRegistry)

	bridge := newRemoteBridge(cec, mqtt, devices)

	if haBridge, ok := container.Get("home-assistant").(*HomeAssistantBridge); ok {
		bridge.haBridge = haBridge
		haBridge.RegisterBirthHandler(bridge.resendDiscovery)
		mqtt.RegisterConnectedHandler(bridge.resendDiscovery)
	}

	devices.RegisterDeviceAddedHandler(bridge.deviceAdded)
	cec.RegisterMessageHandler(bridge.handleUserControlPressed, gocec.OpcodeUserControlPressed)

	container.Register("bridge.remote", bridge)
}

func (bridge *RemoteBridge) deviceAdded(device *Device) {
	bridge.mqtt.Subscribe(bridge.mqtt.BuildTopic(device, "key/set"), 0, func(payload []byte) {
		bridge.handleKeyRequest(device, payload)
	})

	// The TV is the device which forwards its remote control, so register its
	// event entity right away. Others are registered on their first key press.
	if device.LogicalAddress == gocec.DeviceTV {
		bridge.ensureEventEntity(device)
	}
}

func (bridge *RemoteBridge) handleKeyRequest(device *Device, payload []byte) {
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

func (bridge *RemoteBridge) handleUserControlPressed(message gocec.Message) {
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

func (bridge *RemoteBridge) ensureEventEntity(device *Device) {
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

func (bridge *RemoteBridge) resendDiscovery() {
	bridge.mux.Lock()
	devices := make([]*Device, 0, len(bridge.eventEntities))
	for _, device := range bridge.eventEntities {
		devices = append(devices, device)
	}
	bridge.mux.Unlock()

	for _, device := range devices {
		bridge.haBridge.RegisterEvent(device, "remote_key", KeyNames())
	}
}
