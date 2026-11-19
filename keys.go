package main

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/RobertMe/gocec"
)

// keyCodes maps key names to CEC user control codes (CEC 1.4, table 27).
// Codes which require additional operands (like "Play Function" or "Tune
// Function") are left out.
var keyCodes = map[string]byte{
	"select":                    0x00,
	"up":                        0x01,
	"down":                      0x02,
	"left":                      0x03,
	"right":                     0x04,
	"right_up":                  0x05,
	"right_down":                0x06,
	"left_up":                   0x07,
	"left_down":                 0x08,
	"root_menu":                 0x09,
	"setup_menu":                0x0A,
	"contents_menu":             0x0B,
	"favorite_menu":             0x0C,
	"exit":                      0x0D,
	"top_menu":                  0x10,
	"dvd_menu":                  0x11,
	"number_entry_mode":         0x1D,
	"number_11":                 0x1E,
	"number_12":                 0x1F,
	"number_0":                  0x20,
	"number_1":                  0x21,
	"number_2":                  0x22,
	"number_3":                  0x23,
	"number_4":                  0x24,
	"number_5":                  0x25,
	"number_6":                  0x26,
	"number_7":                  0x27,
	"number_8":                  0x28,
	"number_9":                  0x29,
	"dot":                       0x2A,
	"enter":                     0x2B,
	"clear":                     0x2C,
	"next_favorite":             0x2F,
	"channel_up":                0x30,
	"channel_down":              0x31,
	"previous_channel":          0x32,
	"sound_select":              0x33,
	"input_select":              0x34,
	"display_information":       0x35,
	"help":                      0x36,
	"page_up":                   0x37,
	"page_down":                 0x38,
	"power":                     0x40,
	"volume_up":                 0x41,
	"volume_down":               0x42,
	"mute":                      0x43,
	"play":                      0x44,
	"stop":                      0x45,
	"pause":                     0x46,
	"record":                    0x47,
	"rewind":                    0x48,
	"fast_forward":              0x49,
	"eject":                     0x4A,
	"forward":                   0x4B,
	"backward":                  0x4C,
	"stop_record":               0x4D,
	"pause_record":              0x4E,
	"angle":                     0x50,
	"sub_picture":               0x51,
	"video_on_demand":           0x52,
	"electronic_program_guide":  0x53,
	"timer_programming":         0x54,
	"initial_configuration":     0x55,
	"select_broadcast_type":     0x56,
	"select_sound_presentation": 0x57,
	"pause_play_function":       0x61,
	"record_function":           0x62,
	"pause_record_function":     0x63,
	"stop_function":             0x64,
	"mute_function":             0x65,
	"restore_volume_function":   0x66,
	"power_toggle_function":     0x6B,
	"power_off_function":        0x6C,
	"power_on_function":         0x6D,
	"f1_blue":                   0x71,
	"f2_red":                    0x72,
	"f3_green":                  0x73,
	"f4_yellow":                 0x74,
	"f5":                        0x75,
	"data":                      0x76,
}

var keyNames = func() map[byte]string {
	names := make(map[byte]string, len(keyCodes))
	for name, code := range keyCodes {
		names[code] = name
	}
	return names
}()

// KeyCode returns the CEC user control code of the named key.
func KeyCode(name string) (byte, bool) {
	code, ok := keyCodes[name]
	return code, ok
}

// KeyName returns the name of a CEC user control code, or "unknown_0xNN" for
// codes which are not known.
func KeyName(code byte) string {
	if name, ok := keyNames[code]; ok {
		return name
	}
	return fmt.Sprintf("unknown_0x%02X", code)
}

// KeyNames returns all known key names, sorted alphabetically.
func KeyNames() []string {
	names := make([]string, 0, len(keyCodes))
	for name := range keyCodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// sendKey sends a key press (User Control Pressed followed by User Control
// Released) from the adapter to the destination.
func sendKey(cec cecSender, destination gocec.LogicalAddress, code byte) {
	slog.Debug("Sending key press",
		"destination", destination,
		"key", KeyName(code),
	)

	source := cec.AdapterAddress()
	cec.Transmit(gocec.NewMessage(source, destination, gocec.OpcodeUserControlPressed, []byte{code}))
	cec.Transmit(gocec.NewMessage(source, destination, gocec.OpcodeUserControlRelease, []byte{}))
}
