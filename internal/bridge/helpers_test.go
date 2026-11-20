package bridge

import (
	"github.com/splattner/cec2mqtt/internal/cec"
	"github.com/splattner/cec2mqtt/internal/config"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
	"github.com/splattner/cec2mqtt/internal/mqtt"
)

// The real CEC connection and MQTT client must implement what the bridges use.
var (
	_ CEC  = (*cec.Connection)(nil)
	_ MQTT = (*mqtt.Client)(nil)
)

// newTestDiscovery returns a Home Assistant bridge which publishes right away.
func newTestDiscovery(mqtt homeassistant.MQTT) *homeassistant.Discovery {
	cfg := &config.Config{
		Mqtt:          config.Mqtt{BaseTopic: "cec2mqtt"},
		HomeAssistant: config.HomeAssistant{Enable: true, DiscoveryPrefix: "homeassistant"},
	}
	discovery := homeassistant.New(cfg, mqtt)
	discovery.PublishDelay = 0
	return discovery
}
