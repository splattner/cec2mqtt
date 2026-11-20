// Package cec wraps the libcec connection: it finds and opens the adapter,
// adds the devices on the bus to the registry, and passes received messages to
// the registered handlers.
package cec

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/gocec"
	"github.com/splattner/cec2mqtt/internal/logging"
)

type MessageHandler = func(message gocec.Message)

type Connection struct {
	connection *gocec.Connection
	adapter    gocec.Adapter
	// adapterAddress is the logical address of the CEC adapter, known after Start.
	adapterAddress gocec.LogicalAddress
	// started is set once the connection is open.
	started atomic.Bool

	devices                 *device.Registry
	messageReceivedHandlers map[gocec.Opcode][]MessageHandler
	LibCecLoggingEnabled    bool
}

func New(devices *device.Registry, path string) (*Connection, error) {
	config := gocec.NewConfiguration("cec2mqtt", false)

	config.SetMonitorOnly(false)
	config.SetActivateSource(false)

	cec := &Connection{
		devices:                 devices,
		messageReceivedHandlers: make(map[gocec.Opcode][]MessageHandler),
		adapterAddress:          gocec.DeviceRecodingDevice1,
	}
	config.SetLogCallback(cec.handleLogMessage)

	connection, err := gocec.NewConnection(config)
	if err != nil {
		return nil, err
	}

	adapter, err := selectAdapter(connection.FindAdapters(), path)
	if err != nil {
		connection.Close()
		return nil, err
	}

	cec.connection = connection
	cec.adapter = adapter

	return cec, nil
}

// selectAdapter returns the adapter with the path, or the first adapter when
// path is empty.
func selectAdapter(adapters []gocec.Adapter, path string) (gocec.Adapter, error) {
	var adapter gocec.Adapter

	slog.Debug("Adapters found", "adapters", adapters)

	if len(adapters) == 0 {
		return adapter, errors.New("no CEC adapter found")
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
			return adapter, fmt.Errorf("adapter %s has not been found, available adapters: %s", path, strings.Join(paths, ", "))
		}

		slog.Debug("Matched adapter", "adapter", adapter)
	}

	return adapter, nil
}

func (cec *Connection) RegisterMessageHandler(handler MessageHandler, opcodes ...gocec.Opcode) {
	logging.Trace("Registering message handler", "opcodes", opcodes)

	for _, opcode := range opcodes {
		handlers, ok := cec.messageReceivedHandlers[opcode]
		if !ok {
			cec.messageReceivedHandlers[opcode] = []MessageHandler{handler}
		} else {
			cec.messageReceivedHandlers[opcode] = append(handlers, handler)
		}
	}
}

func (cec *Connection) Start() error {
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
func (cec *Connection) Scan() {
	if !cec.started.Load() {
		slog.Warn("Not scanning for devices, the CEC connection isn't open yet")
		return
	}

	addresses := cec.connection.ActiveDevices()
	slog.Debug("Scanned for active devices", "count", len(addresses))

	for _, address := range addresses {
		// Don't register a device for the CEC adapter
		if address != cec.adapterAddress {
			logging.Trace("Found active device", "logical_address", address)
			_ = cec.GetDevice(address)
		} else {
			logging.Trace("Skipping active device as it's the adapter", "logical_address", address)
		}
	}
}

// AdapterPath returns the path of the CEC adapter, like /dev/cec0.
func (cec *Connection) AdapterPath() string {
	return cec.adapter.Path
}

func (cec *Connection) handleLogMessage(logMessage *gocec.LogMessage) {
	if cec.LibCecLoggingEnabled {
		slog.Debug("Incoming message from libcec",
			"message", logMessage.Message,
			"libcec_uptime", logMessage.Uptime,
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

func (cec *Connection) GetDevice(address gocec.LogicalAddress) *device.Device {
	if address == gocec.DeviceBroadcast {
		return nil
	}

	creator := func() *device.Description {
		return &device.Description{
			LogicalAddress:  address,
			PhysicalAddress: cec.connection.GetPhysicalAddress(address),
			Vendor:          cec.connection.GetVendor(address),
			OSD:             cec.connection.GetOSDName(address),
		}
	}

	return cec.devices.GetByCecDevice(address, creator)
}

func (cec *Connection) Transmit(message gocec.Message) {
	logging.Trace("Transmitting CEC message",
		"message.text", message.String(),
		"message.raw", []byte(message),
	)
	if err := cec.connection.Transmit(message); err != nil {
		// Normal for devices which are off, so not a warning.
		slog.Debug("Failed to transmit CEC message",
			"message.text", message.String(),
			"error", err,
		)
	}
}

// Close closes the CEC adapter, so it unregisters from the bus, and frees
// libcec. Bridges must be stopped before.
func (cec *Connection) Close() {
	cec.started.Store(false)
	cec.connection.Close()
	slog.Info("Closed CEC connection")
}

// AdapterAddress returns the logical address of the CEC adapter.
func (cec *Connection) AdapterAddress() gocec.LogicalAddress {
	return cec.adapterAddress
}

func (cec *Connection) PowerOn(address gocec.LogicalAddress) {
	cec.connection.PowerOnDevice(address)
}

func (cec *Connection) Standby(address gocec.LogicalAddress) {
	cec.connection.StandByDevice(address)
}

func (cec *Connection) PowerStatus(address gocec.LogicalAddress) gocec.PowerStatus {
	return cec.connection.GetPowerStatus(address)
}

func (cec *Connection) ActiveSource() gocec.LogicalAddress {
	return cec.connection.GetActiveSource()
}
