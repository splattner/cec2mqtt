package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

type MessageReceivedHandler func(message gocec.Message)

type Cec struct {
	connection *gocec.Connection
	adapter    gocec.Adapter
	// adapterAddress is the logical address of the CEC adapter, known after Start.
	adapterAddress gocec.LogicalAddress
	// started is set once the connection is open.
	started atomic.Bool

	devices                 *DeviceRegistry
	messageReceivedHandlers map[gocec.Opcode][]MessageReceivedHandler
	LibCecLoggingEnabled    bool
}

type CecDeviceDescription struct {
	logicalAddress  gocec.LogicalAddress
	physicalAddress gocec.PhysicalAddress
	vendor          gocec.Vendor
	OSD             string
}

func InitialiseCec(devices *DeviceRegistry, path string) (*Cec, error) {
	config := gocec.NewConfiguration("cec2mqtt", false)

	config.SetMonitorOnly(false)
	config.SetActivateSource(false)

	cec := &Cec{
		devices:                 devices,
		messageReceivedHandlers: make(map[gocec.Opcode][]MessageReceivedHandler),
		adapterAddress:          gocec.DeviceRecodingDevice1,
	}
	config.SetLogCallback(cec.handleLogMessage)

	connection, err := gocec.NewConnection(config)
	if err != nil {
		return nil, err
	}

	var adapter gocec.Adapter
	adapters := connection.FindAdapters()

	slog.Debug("Adapters found", "adapters", adapters)

	if len(adapters) == 0 {
		return nil, errors.New("no CEC adapter found")
	}

	if len(path) == 0 {
		adapter = adapters[0]

		slog.Debug("Using the first available CEC adapter", "adapter", adapter)

	} else {
		var found bool
		for _, candidate := range adapters {
			if candidate.Path == path {
				adapter = candidate
				found = true
				break
			}
		}

		if !found {
			paths := make([]string, len(adapters))
			for i, candidate := range adapters {
				paths[i] = candidate.Path
			}
			return nil, fmt.Errorf("adapter %s has not been found, available adapters: %s", path, strings.Join(paths, ", "))
		}

		slog.Debug("Matched adapter", "adapter", adapter)
	}

	cec.connection = connection
	cec.adapter = adapter

	return cec, nil
}

func (cec *Cec) RegisterMessageHandler(handler MessageReceivedHandler, opcodes ...gocec.Opcode) {
	logTrace("Registering message handler", "opcodes", opcodes)

	for _, opcode := range opcodes {
		handlers, ok := cec.messageReceivedHandlers[opcode]
		if !ok {
			cec.messageReceivedHandlers[opcode] = []MessageReceivedHandler{handler}
		} else {
			cec.messageReceivedHandlers[opcode] = append(handlers, handler)
		}
	}
}

func (cec *Cec) Start() error {
	if err := cec.connection.Open(cec.adapter); err != nil {
		return err
	}

	slog.Info("Opened CEC connection", "adapter", cec.adapter)

	adapterAddress, err := cec.connection.GetAdapterAddress()
	if err != nil {
		slog.Warn("Could not determine the logical address of the CEC adapter", "error", err)
	}
	cec.adapterAddress = adapterAddress
	slog.Info("CEC adapter registered", "logical_address", adapterAddress)

	cec.started.Store(true)
	cec.Scan()

	return nil
}

// Scan polls all logical addresses and adds the devices which answer and
// aren't known yet. It takes a few seconds, and does nothing before Start.
func (cec *Cec) Scan() {
	if !cec.started.Load() {
		slog.Warn("Not scanning for devices, the CEC connection isn't open yet")
		return
	}

	addresses := cec.connection.ActiveDevices()
	slog.Debug("Scanned for active devices", "count", len(addresses))

	for _, address := range addresses {
		// Don't register a device for the CEC adapter
		if address != cec.adapterAddress {
			logTrace("Found active device", "logical_address", address)
			_ = cec.GetDevice(address)
		} else {
			logTrace("Skipping active device as it's the adapter", "logical_address", address)
		}
	}
}

// AdapterPath returns the path of the CEC adapter, like /dev/cec0.
func (cec *Cec) AdapterPath() string {
	return cec.adapter.Path
}

func (cec *Cec) handleLogMessage(logMessage *gocec.LogMessage) {
	if cec.LibCecLoggingEnabled {
		slog.Debug("Incoming message from libcec",
			"message", logMessage.Message,
			// gocec converts libcec's milliseconds since start as if they were seconds since the Unix epoch.
			"libcec_uptime", time.Duration(logMessage.Time.Unix())*time.Millisecond,
			"level", logMessage.Level,
		)
	}

	if logMessage.Level != gocec.LogLevelTraffic {
		return
	}

	if !strings.HasPrefix(logMessage.Message, ">> ") {
		return
	}

	message, err := gocec.ParseMessage(logMessage.Message[3:])
	if err != nil || len(message) == 0 {
		slog.Debug("Ignoring CEC message which could not be parsed",
			"raw_message", logMessage.Message[3:],
			"error", err,
		)
		return
	}

	device := cec.GetDevice(message.Source())

	if device == nil {
		return
	}

	context := slog.With(
		"raw_message", logMessage.Message[3:],
		"parsed_message", message,
		"source.logical_address", message.Source(),
		"source.device.id", device.Id,
		"opcode", message.Opcode(),
	)

	context.Debug("Message received")

	if handlers, ok := cec.messageReceivedHandlers[message.Opcode()]; ok {
		for _, handler := range handlers {
			context.Debug("Invoking handler")

			handler(message)
		}
	}
}

func (cec *Cec) GetDevice(address gocec.LogicalAddress) *Device {
	if address == gocec.DeviceBroadcast {
		return nil
	}

	creator := func() *CecDeviceDescription {
		return &CecDeviceDescription{
			logicalAddress:  address,
			physicalAddress: cec.connection.GetPhysicalAddress(address),
			vendor:          cec.connection.GetVendor(address),
			OSD:             cec.connection.GetOSDName(address),
		}
	}

	return cec.devices.GetByCecDevice(address, creator)
}

func (cec *Cec) Transmit(message gocec.Message) {
	logTrace("Transmitting CEC message",
		"message.text", message.String(),
		"message.raw", []byte(message),
	)
	cec.connection.Transmit(message)
}

// AdapterAddress returns the logical address of the CEC adapter.
func (cec *Cec) AdapterAddress() gocec.LogicalAddress {
	return cec.adapterAddress
}

func (cec *Cec) PowerOn(address gocec.LogicalAddress) {
	cec.connection.PowerOnDevice(address)
}

func (cec *Cec) Standby(address gocec.LogicalAddress) {
	cec.connection.StandByDevice(address)
}

func (cec *Cec) PowerStatus(address gocec.LogicalAddress) gocec.PowerStatus {
	return cec.connection.GetPowerStatus(address)
}

func (cec *Cec) ActiveSource() gocec.LogicalAddress {
	return cec.connection.GetActiveSource()
}
