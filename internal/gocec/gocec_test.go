package gocec

import (
	"strings"
	"sync"
	"testing"
)

// TestStringConcurrent converts names from several goroutines at the same
// time. Earlier versions shared one C buffer, which garbled the names. (The
// race detector doesn't see that, the buffer was C memory.)
func TestStringConcurrent(t *testing.T) {
	stringers := []interface{ String() string }{
		VendorSamsung, VendorLg, VendorSony, VendorPhilips,
		OpcodeStandby, OpcodeReportPowerStatus, OpcodeSetStreamPath,
		DeviceTV, DevicePlaybackDevice1, DeviceAudiosystem,
		PowerStatusOn, PowerStatusStandBy,
	}

	var wg sync.WaitGroup
	for _, stringer := range stringers {
		want := stringer.String()
		if want == "" {
			t.Errorf("%#v has no name", stringer)
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if got := stringer.String(); got != want {
					t.Errorf("String() = %q, want %q", got, want)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestNames(t *testing.T) {
	tests := map[string]interface{ String() string }{
		"Samsung":    VendorSamsung,
		"standby":    OpcodeStandby,
		"TV":         DeviceTV,
		"Playback 1": DevicePlaybackDevice1,
		"on":         PowerStatusOn,
		"1.2.0.0":    PhysicalAddress{0x12, 0x00},
	}
	for want, stringer := range tests {
		if got := stringer.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}

func TestMessage(t *testing.T) {
	message := NewMessage(DeviceRecodingDevice1, DeviceTV, OpcodeSetStreamPath, []byte{0x10, 0x00})

	if string(message) != "\x10\x86\x10\x00" {
		t.Errorf("NewMessage = %X", []byte(message))
	}
	if message.Source() != DeviceRecodingDevice1 || message.Destination() != DeviceTV || message.Opcode() != OpcodeSetStreamPath {
		t.Errorf("unexpected source %d, destination %d or opcode %X", message.Source(), message.Destination(), byte(message.Opcode()))
	}
	if string(message.Parameters()) != "\x10\x00" {
		t.Errorf("Parameters = %X", message.Parameters())
	}
	if !strings.Contains(message.String(), "with parameters: 1000") {
		t.Errorf("String = %q", message.String())
	}

	parsed, err := ParseMessage("10:86:10:00")
	if err != nil || string(parsed) != string(message) {
		t.Errorf("ParseMessage = %X, %v", []byte(parsed), err)
	}
	if _, err := ParseMessage("1x:86"); err == nil {
		t.Error("expected an error for an invalid message")
	}
}

func TestShortMessages(t *testing.T) {
	// A poll message only has the addresses.
	poll := Message{0x10}
	if poll.Source() != DeviceRecodingDevice1 || poll.Destination() != DeviceTV || poll.Opcode() != OpcodeNone || poll.Parameters() != nil {
		t.Errorf("unexpected poll message: %d %d %X %X", poll.Source(), poll.Destination(), byte(poll.Opcode()), poll.Parameters())
	}

	// Earlier versions panicked on these.
	empty := Message{}
	if empty.Source() != DeviceUnknown || empty.Destination() != DeviceUnknown || empty.Opcode() != OpcodeNone {
		t.Error("unexpected values for an empty message")
	}
	if empty.String() != "empty message" {
		t.Errorf("String = %q", empty.String())
	}
}

func TestNewCommand(t *testing.T) {
	command, err := newCommand(NewMessage(DeviceRecodingDevice1, DeviceBroadcast, OpcodeStandby, nil))
	if err != nil {
		t.Fatal(err)
	}
	if command.initiator != 1 || command.destination != 15 || command.opcode_set != 1 || command.opcode != 0x36 || command.parameters.size != 0 {
		t.Errorf("unexpected command %+v", command)
	}

	command, err = newCommand(Message{0x10})
	if err != nil || command.opcode_set != 0 {
		t.Errorf("poll message: %+v, %v", command, err)
	}

	command, err = newCommand(NewMessage(DeviceRecodingDevice1, DeviceTV, OpcodeSetOsdString, make([]byte, maxParameters)))
	if err != nil || command.parameters.size != maxParameters {
		t.Errorf("maximum parameters: size %d, %v", command.parameters.size, err)
	}

	// Earlier versions panicked on too many parameters.
	if _, err := newCommand(NewMessage(DeviceRecodingDevice1, DeviceTV, OpcodeSetOsdString, make([]byte, maxParameters+1))); err == nil {
		t.Error("expected an error for too many parameters")
	}
	if _, err := newCommand(Message{}); err == nil {
		t.Error("expected an error for an empty message")
	}
}

func TestConnectionClose(t *testing.T) {
	var logs []string
	var mux sync.Mutex

	config := NewConfiguration("gocec-test", false)
	config.SetLogCallback(func(message *LogMessage) {
		mux.Lock()
		defer mux.Unlock()
		logs = append(logs, message.Message)
	})

	connection, err := NewConnection(config)
	if err != nil {
		t.Fatal(err)
	}

	// No adapter needed: libcec is initialised without opening one.
	connection.Close()
	connection.Close() // safe to call again

	mux.Lock()
	defer mux.Unlock()
	if lookupCallbacks(config.callbacksIndex) != nil {
		t.Error("callbacks not released")
	}
}
