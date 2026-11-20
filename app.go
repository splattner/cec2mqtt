package main

// App holds the bridges between CEC and MQTT, wired together explicitly so the
// order they're set up and shut down in is visible here.
type App struct {
	// homeAssistant is nil when the Home Assistant integration is disabled.
	homeAssistant *HomeAssistantBridge
	activeSource  *ActiveSourceBridge
	audio         *AudioBridge
	power         *PowerBridge
	remote        *RemoteBridge
}

// newApp sets up all bridges. Devices found on the CEC bus afterwards are
// passed to every bridge, so the CEC connection must be started after this.
func newApp(config *Config, devices *DeviceRegistry, mqtt mqttClient, cec cecBus) *App {
	app := &App{}

	// First, as the other bridges register their entities with it.
	app.homeAssistant = setupHomeAssistantBridge(config, mqtt)

	app.activeSource = setupActiveSourceBridge(cec, mqtt, devices, app.homeAssistant)
	app.audio = setupAudioBridge(cec, mqtt, devices, app.homeAssistant)
	app.power = setupPowerBridge(cec, mqtt, devices, app.homeAssistant)
	app.remote = setupRemoteBridge(cec, mqtt, devices, app.homeAssistant)

	return app
}

// Close stops the bridges, in reverse order of setup. The MQTT and CEC
// connections are left open.
func (app *App) Close() {
	app.power.Close()
	app.audio.Close()
	app.activeSource.Close()
	if app.homeAssistant != nil {
		app.homeAssistant.Close()
	}
}
