// Package bridge connects the CEC bus to MQTT: one bridge per feature (power,
// active source, audio, remote control keys, diagnostics and the bus wide
// controls), set up together by New.
package bridge

import (
	"github.com/splattner/cec2mqtt/internal/config"
	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/homeassistant"
)

// Bridges holds the bridges between CEC and MQTT, wired together explicitly so the
// order they're set up and shut down in is visible here.
type Bridges struct {
	// homeAssistant is nil when the Home Assistant integration is disabled.
	homeAssistant *homeassistant.Discovery
	activeSource  *ActiveSource
	audio         *Audio
	power         *Power
	remote        *Remote
	diagnostics   *Diagnostics
	controls      *Controls
}

// New sets up all bridges. Devices found on the CEC bus afterwards are
// passed to every bridge, so the CEC connection must be started after this.
func New(config *config.Config, devices *device.Registry, mqtt MQTT, cec CEC) *Bridges {
	app := &Bridges{}

	// First, as the other bridges register their entities with it.
	app.homeAssistant = homeassistant.Setup(config, mqtt)

	app.activeSource = setupActiveSource(cec, mqtt, devices, app.homeAssistant)
	app.audio = setupAudio(cec, mqtt, devices, app.homeAssistant)
	app.power = setupPower(cec, mqtt, devices, app.homeAssistant)
	app.remote = setupRemote(cec, mqtt, devices, app.homeAssistant)
	app.diagnostics = setupDiagnostics(mqtt, devices, app.homeAssistant)
	app.controls = setupControls(config, cec, mqtt, app.homeAssistant, app.power.MonitorAll)

	return app
}

// Close stops the bridges, in reverse order of setup. The MQTT and CEC
// connections are left open.
func (app *Bridges) Close() {
	app.controls.Close()
	app.power.Close()
	app.audio.Close()
	app.activeSource.Close()
	if app.homeAssistant != nil {
		app.homeAssistant.Close()
	}
}
