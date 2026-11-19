package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/RobertMe/gocec"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type DeviceAddedHandler func(device *Device)

type DeviceRegistry struct {
	dataDirectory string
	configDevices map[string]*DeviceConfig

	deviceAddedHandlers []DeviceAddedHandler

	devicesMutex       sync.Mutex
	devices            map[gocec.LogicalAddress]*Device
	physicalAddressMap map[gocec.PhysicalAddress]*Device
}

type DeviceConfig struct {
	Id              string `yaml:"id"`
	PhysicalAddress string `yaml:"physical_address"`
	VendorId        int    `yaml:"vendor_id"`
	OSD             string `yaml:"osd"`
	MqttTopic       string `yaml:"mqtt_topic"`
	Ignore          bool   `yaml:"ignore"`
}

type Device struct {
	Id string

	CecDevice *CecDeviceDescription
	Config    *DeviceConfig

	LogicalAddress gocec.LogicalAddress
}

type CreateCecDeviceDescription func() *CecDeviceDescription

func NewDeviceRegistry(dataDirectory string) *DeviceRegistry {
	return &DeviceRegistry{
		dataDirectory:       dataDirectory,
		configDevices:       loadDevicesFromConfig(dataDirectory),
		deviceAddedHandlers: make([]DeviceAddedHandler, 0),
		devices:             make(map[gocec.LogicalAddress]*Device),
		physicalAddressMap:  make(map[gocec.PhysicalAddress]*Device),
	}
}

func loadDevicesFromConfig(dataDirectory string) (devices map[string]*DeviceConfig) {
	logContext := slog.With("config_file", dataDirectory+"devices.yaml")
	logContext.Info("Reading devices from config file")

	devices = make(map[string]*DeviceConfig)
	data, err := os.ReadFile(dataDirectory + "devices.yaml")
	if err != nil {
		logContext.Info("Devices configuration file does not exist yet")
		return
	}

	err = yaml.Unmarshal(data, &devices)
	if err != nil {
		logContext.Error("Devices configuration file could not be parsed as valid YAML", "error", err)
		return
	}

	return
}

func (registry *DeviceRegistry) RegisterDeviceAddedHandler(handler DeviceAddedHandler) {
	logTrace("Registering device added handler")
	registry.deviceAddedHandlers = append(registry.deviceAddedHandlers, handler)
}

func (registry *DeviceRegistry) FindByLogicalAddress(address gocec.LogicalAddress) *Device {
	logContext := slog.With("logical_address", address)
	registry.devicesMutex.Lock()
	defer registry.devicesMutex.Unlock()
	device, ok := registry.devices[address]
	if !ok {
		logContext.Debug("Could not find device by logical address")
		return nil
	}

	if device.Config.Ignore {
		logContext.Debug("Found device by logical address, but it's ignored")

		return nil
	}

	logContext.Debug("Found device by logical address", "device.id", device.Id)

	return device
}

func (registry *DeviceRegistry) GetByCecDevice(address gocec.LogicalAddress, creator CreateCecDeviceDescription) *Device {
	registry.devicesMutex.Lock()

	logContext := slog.With("logical_address", address)

	if device, ok := registry.devices[address]; ok {
		registry.devicesMutex.Unlock()

		logContext = logContext.With("device.id", device.Config.Id)

		if device.Config.Ignore {
			trace(logContext, "Found device by logical address, but it's ignored")

			return nil
		}

		trace(logContext, "Found device by logical address")

		return device
	}

	description := creator()

	if description.physicalAddress == [2]byte{0xFF, 0xFF} ||
		description.vendor == gocec.VendorUnknown ||
		description.OSD == "cec2mqtt" {
		registry.devicesMutex.Unlock()
		logContext.Debug("Ignoring device because it is incomplete or cec2mqtt",
			"description.physical_address", description.physicalAddress,
			"description.vendor", uint(description.vendor),
			"description.osd", description.OSD,
		)

		return nil
	}

	device, ok := registry.physicalAddressMap[description.physicalAddress]
	if ok {
		if device.CecDevice.physicalAddress == description.physicalAddress &&
			device.CecDevice.vendor == description.vendor {
			registry.devices[address] = device
			registry.devicesMutex.Unlock()

			logContext = logContext.With(
				"physical_address", description.physicalAddress,
				"vendor_id", description.vendor,
				"description", description,
				"device.id", device.Config.Id,
			)

			if device.Config.Ignore {
				logContext.Debug("Found device by physical address and vendor, but it's ignored")

				return nil
			}

			logContext.Debug("Found device by physical address and vendor")

			return device
		}
	}

	deviceConfig := registry.FindDevice(description.physicalAddress.String(), int(description.vendor), description.OSD)

	device = &Device{
		Id:             deviceConfig.Id,
		CecDevice:      description,
		Config:         deviceConfig,
		LogicalAddress: description.logicalAddress,
	}

	registry.devices[device.LogicalAddress] = device
	registry.physicalAddressMap[description.physicalAddress] = device

	// Persist immediately so newly found devices (or updated addresses) survive a crash.
	_ = registry.saveLocked()

	registry.devicesMutex.Unlock()

	logContext = logContext.With(
		"physical_address", description.physicalAddress,
		"vendor", description.vendor,
		"description", description,
		"device.id", device.Config.Id,
	)

	if deviceConfig.Ignore {
		logContext.Debug("Added new device, but not registering it as it's ignored")

		return nil
	}

	logContext.Debug("Adding new device")

	for _, handler := range registry.deviceAddedHandlers {
		handler(device)
	}

	return device
}

// FindDevice looks up the configuration of a device. An exact match on physical
// address, vendor and OSD name always wins. Otherwise a device matching on
// either physical address and vendor, or vendor and OSD name, is used, but only
// when exactly one such device exists. When nothing matches a new device
// configuration is created.
//
// Must be called while holding registry.devicesMutex.
func (registry *DeviceRegistry) FindDevice(physicalAddress string, vendorId int, name string) *DeviceConfig {
	logContext := slog.With(
		"physical_address", physicalAddress,
		"vendor_id", vendorId,
		"name", name,
	)
	trace(logContext, "Searching device in config")

	var candidates []*DeviceConfig
	for _, device := range registry.configDevices {
		if device.VendorId != vendorId {
			continue
		}

		if device.PhysicalAddress == physicalAddress && device.OSD == name {
			logContext.Info("Matched exact device from config", "device.id", device.Id)

			return device
		}

		if device.PhysicalAddress == physicalAddress || device.OSD == name {
			candidates = append(candidates, device)
		}
	}

	var option *DeviceConfig
	switch len(candidates) {
	case 0:
	case 1:
		option = candidates[0]
	default:
		logContext.Debug("Multiple possible matches are found. Creating new device.", "candidates", len(candidates))
	}

	if option != nil {
		logContext.Info("Found device in config",
			"device.id", option.Id,
			"device.physical_address", option.PhysicalAddress,
			"device.vendor_id", option.VendorId,
			"device.osd", option.OSD,
		)

		option.PhysicalAddress = physicalAddress
		option.VendorId = vendorId
		option.OSD = name
		return option
	}

	var id string
	for {
		id = uuid.New().String()

		if _, ok := registry.configDevices[id]; !ok {
			break
		}
	}

	device := &DeviceConfig{
		Id:              id,
		PhysicalAddress: physicalAddress,
		VendorId:        vendorId,
		OSD:             name,
		MqttTopic:       name,
	}

	slog.Info("Created new device",
		"device.id", device.Id,
		"device.physical_address", device.PhysicalAddress,
		"device.vendor_id", device.VendorId,
		"device.osd", device.OSD,
		"device.mqtt_topic", device.MqttTopic,
	)

	registry.configDevices[id] = device

	return device
}

// Save writes the devices configuration to devices.yaml in the data directory.
func (registry *DeviceRegistry) Save() error {
	registry.devicesMutex.Lock()
	defer registry.devicesMutex.Unlock()

	return registry.saveLocked()
}

// saveLocked must be called while holding registry.devicesMutex.
func (registry *DeviceRegistry) saveLocked() error {
	path := registry.dataDirectory + "devices.yaml"
	logContext := slog.With("config_file", path)

	data, err := yaml.Marshal(registry.configDevices)
	if err != nil {
		logContext.Error("Failed to convert devices configuration into YAML", "error", err)
		return err
	}

	if err := writeFileAtomic(path, data, 0644); err != nil {
		logContext.Error("Failed to save devices configuration to file", "error", err)
		return err
	}

	logContext.Debug("Saved devices configuration")

	return nil
}

// writeFileAtomic writes data to a temporary file first and then renames it, so
// the target file is never left half written.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

func (registry *DeviceRegistry) List() []*Device {
	registry.devicesMutex.Lock()
	defer registry.devicesMutex.Unlock()

	devices := make([]*Device, 0, len(registry.devices))
	for _, device := range registry.devices {
		devices = append(devices, device)
	}
	return devices
}
