package homeassistant

import "github.com/splattner/cec2mqtt/internal/config"

// newTestDiscovery returns a bridge which publishes right away.
func newTestDiscovery(mqtt MQTT) *Discovery {
	cfg := &config.Config{
		Mqtt:          config.Mqtt{BaseTopic: "cec2mqtt"},
		HomeAssistant: config.HomeAssistant{Enable: true, DiscoveryPrefix: "homeassistant"},
	}
	discovery := New(cfg, mqtt)
	discovery.PublishDelay = 0
	return discovery
}
