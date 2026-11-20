package device

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/logging"
)

type AddedHandler func(device *Device)

type Registry struct {
	dataDirectory string
	configDevices map[string]*Config

	deviceAddedHandlers []AddedHandler

	devicesMutex       sync.Mutex
	devices            map[gocec.LogicalAddress]*Device
	physicalAddressMap map[gocec.PhysicalAddress]*Device
}

type DescriptionFunc func() *Description

func NewRegistry(dataDirectory string) *Registry {
	return &Registry{
		dataDirectory:       dataDirectory,
		configDevices:       loadDevicesFromConfig(dataDirectory),
		deviceAddedHandlers: make([]AddedHandler, 0),
		devices:             make(map[gocec.LogicalAddress]*Device),
		physicalAddressMap:  make(map[gocec.PhysicalAddress]*Device),
	}
}

func loadDevicesFromConfig(dataDirectory string) (devices map[string]*Config) {
	logContext := slog.With("config_file", dataDirectory+"devices.yaml")
	logContext.Info("Reading devices from config file")

	devices = make(map[string]*Config)
	data, err := os.ReadFile(dataDirectory + "devices.yaml") //nolint:gosec // the data directory is chosen with -data-dir
	if errors.Is(err, fs.ErrNotExist) {
		logContext.Info("Devices configuration file does not exist yet")
		return
	}
	if err != nil {
		logContext.Error("Devices configuration file could not be read", "error", err)
		return
	}

	err = yaml.Unmarshal(data, &devices)
	if err != nil {
		logContext.Error("Devices configuration file could not be parsed as valid YAML", "error", err)
		return
	}

	return
}

func (registry *Registry) RegisterDeviceAddedHandler(handler AddedHandler) {
	logging.Trace("Registering device added handler")
	registry.deviceAddedHandlers = append(registry.deviceAddedHandlers, handler)
}

func (registry *Registry) FindByLogicalAddress(address gocec.LogicalAddress) *Device {
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

func (registry *Registry) GetByCecDevice(address gocec.LogicalAddress, creator DescriptionFunc) *Device {
	registry.devicesMutex.Lock()

	logContext := slog.With("logical_address", address)

	if device, ok := registry.devices[address]; ok {
		registry.devicesMutex.Unlock()

		logContext = logContext.With("device.id", device.Config.Id)

		if device.Config.Ignore {
			logging.TraceWith(logContext, "Found device by logical address, but it's ignored")

			return nil
		}

		logging.TraceWith(logContext, "Found device by logical address")

		return device
	}

	description := creator()

	if description.PhysicalAddress == [2]byte{0xFF, 0xFF} ||
		description.Vendor == gocec.VendorUnknown ||
		description.OSD == "cec2mqtt" {
		registry.devicesMutex.Unlock()
		logContext.Debug("Ignoring device because it is incomplete or cec2mqtt",
			"description.physical_address", description.PhysicalAddress,
			"description.Vendor", uint(description.Vendor),
			"description.osd", description.OSD,
		)

		return nil
	}

	device, ok := registry.physicalAddressMap[description.PhysicalAddress]
	if ok {
		if device.CecDevice.PhysicalAddress == description.PhysicalAddress &&
			device.CecDevice.Vendor == description.Vendor {
			registry.devices[address] = device
			registry.devicesMutex.Unlock()

			logContext = logContext.With(
				"physical_address", description.PhysicalAddress,
				"vendor_id", description.Vendor,
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

	deviceConfig := registry.FindDevice(description.PhysicalAddress.String(), int(description.Vendor), description.OSD)

	device = &Device{
		Id:             deviceConfig.Id,
		CecDevice:      description,
		Config:         deviceConfig,
		LogicalAddress: description.LogicalAddress,
	}

	registry.devices[device.LogicalAddress] = device
	registry.physicalAddressMap[description.PhysicalAddress] = device

	// Persist immediately so newly found devices (or updated addresses) survive a crash.
	_ = registry.saveLocked()

	registry.devicesMutex.Unlock()

	logContext = logContext.With(
		"physical_address", description.PhysicalAddress,
		"vendor", description.Vendor,
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
func (registry *Registry) FindDevice(physicalAddress string, vendorId int, name string) *Config {
	logContext := slog.With(
		"physical_address", physicalAddress,
		"vendor_id", vendorId,
		"name", name,
	)
	logging.TraceWith(logContext, "Searching device in config")

	var candidates []*Config
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

	var option *Config
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

	mqttTopic := name
	if mqttTopic == ReservedTopic {
		// Used by the Cec2Mqtt bridge itself.
		mqttTopic = name + "_device"
	}

	device := &Config{
		Id:              id,
		PhysicalAddress: physicalAddress,
		VendorId:        vendorId,
		OSD:             name,
		MqttTopic:       mqttTopic,
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
func (registry *Registry) Save() error {
	registry.devicesMutex.Lock()
	defer registry.devicesMutex.Unlock()

	return registry.saveLocked()
}

// saveLocked must be called while holding registry.devicesMutex.
func (registry *Registry) saveLocked() error {
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
	// Fails once the file was renamed, which is expected.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one to report
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close() // the chmod error is the one to report
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

func (registry *Registry) List() []*Device {
	registry.devicesMutex.Lock()
	defer registry.devicesMutex.Unlock()

	devices := make([]*Device, 0, len(registry.devices))
	for _, device := range registry.devices {
		devices = append(devices, device)
	}
	return devices
}
