package gocec

/*
#cgo pkg-config: libcec
#cgo LDFLAGS: -Wl,-rpath=/opt/vc/lib
#include <stdlib.h>
#include <libcec/cecc.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type Connection struct {
	connection C.libcec_connection_t
	config     *Configuration

	closeOnce sync.Once
}

type Adapter struct {
	Path, Comm string
}

func NewConnection(config *Configuration) (*Connection, error) {
	connection := C.libcec_initialise(&config.configuration)
	if connection == nil {
		return nil, errors.New("failed to initialise libcec")
	}

	return &Connection{connection: connection, config: config}, nil
}

// Close closes the adapter, if it was opened, and frees libcec. The
// connection must not be used afterwards. It's safe to call more than once.
func (conn *Connection) Close() {
	conn.closeOnce.Do(func() {
		C.libcec_close(conn.connection)
		C.libcec_destroy(conn.connection)
		conn.connection = nil

		// Only now, as libcec logs while closing.
		releaseCallbacks(conn.config)
	})
}

func (conn *Connection) FindAdapters() []Adapter {
	var foundDevices [10]C.cec_adapter
	count := int(C.libcec_find_adapters(conn.connection, &foundDevices[0], C.uchar(len(foundDevices)), nil))

	adapters := make([]Adapter, count)
	for i := 0; i < count; i++ {
		adapters[i] = Adapter{
			Path: C.GoString(&foundDevices[i].path[0]),
			Comm: C.GoString(&foundDevices[i].comm[0]),
		}
	}

	return adapters
}

func (conn *Connection) Open(adapter Adapter) error {
	comm := C.CString(adapter.Comm)
	defer C.free(unsafe.Pointer(comm))

	result := C.libcec_open(conn.connection, comm, C.CEC_DEFAULT_CONNECT_TIMEOUT)

	if result < 1 {
		return fmt.Errorf("failed to open adapter %s", adapter.Comm)
	}

	return nil
}

func (conn *Connection) GetAdapterAddress() (LogicalAddress, error) {
	addresses := C.libcec_get_logical_addresses(conn.connection)
	// primary is CECDEVICE_UNKNOWN (-1) when the adapter has no address.
	primary := int(addresses.primary)
	if primary < 0 || primary >= len(addresses.addresses) || addresses.addresses[primary] == 0 {
		return DeviceUnregistered, errors.New("unable to determine logical address of the CEC adapter")
	}
	return LogicalAddress(primary), nil
}

func (conn *Connection) ActiveDevices() []LogicalAddress {
	activeDevices := C.libcec_get_active_devices(conn.connection)

	result := make([]LogicalAddress, 0, 16)
	for address, active := range activeDevices.addresses {
		if active == 1 {
			result = append(result, LogicalAddress(address))
		}
	}

	return result
}

func (conn *Connection) GetPowerStatus(address LogicalAddress) PowerStatus {
	return PowerStatus(C.libcec_get_device_power_status(conn.connection, C.cec_logical_address(address)))
}

func (conn *Connection) GetPhysicalAddress(address LogicalAddress) PhysicalAddress {
	physical := C.libcec_get_device_physical_address(conn.connection, C.cec_logical_address(address))
	return PhysicalAddress{byte(physical >> 8), byte(physical & 0xFF)}
}

func (conn *Connection) GetVendor(address LogicalAddress) Vendor {
	vendor := C.libcec_get_device_vendor_id(conn.connection, C.cec_logical_address(address))
	return Vendor(vendor)
}

func (conn *Connection) GetOSDName(address LogicalAddress) string {
	name := C.cec_osd_name{}
	C.libcec_get_device_osd_name(conn.connection, C.cec_logical_address(address), (*C.char)(unsafe.Pointer(&name[0])))
	return C.GoString((*C.char)(unsafe.Pointer(&name[0])))
}

func (conn *Connection) PowerOnDevice(address LogicalAddress) {
	C.libcec_power_on_devices(conn.connection, C.cec_logical_address(address))
}

func (conn *Connection) StandByDevice(address LogicalAddress) {
	C.libcec_standby_devices(conn.connection, C.cec_logical_address(address))
}

func (conn *Connection) GetActiveSource() LogicalAddress {
	return LogicalAddress(C.libcec_get_active_source(conn.connection))
}

// maxParameters is the number of parameters a CEC message can have at most in
// libcec (CEC_MAX_DATA_PACKET_SIZE).
const maxParameters = 64

// Transmit sends the message. It returns an error when the message is invalid,
// or when it wasn't acknowledged, e.g. because the destination is off.
func (conn *Connection) Transmit(message Message) error {
	command, err := newCommand(message)
	if err != nil {
		return err
	}

	if C.libcec_transmit(conn.connection, &command) == 0 {
		return fmt.Errorf("message %X was not acknowledged", []byte(message))
	}

	return nil
}

func newCommand(message Message) (C.cec_command, error) {
	var command C.cec_command

	if len(message) == 0 {
		return command, errors.New("can't send an empty message")
	}

	parameters := message.Parameters()
	if len(parameters) > maxParameters {
		return command, fmt.Errorf("message has %d parameters, at most %d are possible", len(parameters), maxParameters)
	}

	command.initiator = C.cec_logical_address(message.Source())
	command.destination = C.cec_logical_address(message.Destination())

	if len(message) > 1 {
		command.opcode_set = 1
		command.opcode = C.cec_opcode(message.Opcode())
	}

	command.parameters.size = C.uint8_t(len(parameters))
	for i, val := range parameters {
		command.parameters.data[i] = C.uint8_t(val)
	}

	return command, nil
}
