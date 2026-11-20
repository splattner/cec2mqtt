// Package device keeps track of the devices on the CEC bus, and their
// configuration in devices.yaml.
package device

import "github.com/splattner/cec2mqtt/internal/gocec"

// ReservedTopic is the MQTT topic name of the Cec2Mqtt bridge itself, which a
// device can't use.
const ReservedTopic = "bridge"

type Config struct {
	Id              string `yaml:"id"`
	PhysicalAddress string `yaml:"physical_address"`
	VendorId        int    `yaml:"vendor_id"`
	OSD             string `yaml:"osd"`
	MqttTopic       string `yaml:"mqtt_topic"`
	Ignore          bool   `yaml:"ignore"`
}

type Device struct {
	Id string

	CecDevice *Description
	Config    *Config

	LogicalAddress gocec.LogicalAddress
}

// Description is how a device describes itself on the CEC bus.
type Description struct {
	LogicalAddress  gocec.LogicalAddress
	PhysicalAddress gocec.PhysicalAddress
	Vendor          gocec.Vendor
	OSD             string
}
