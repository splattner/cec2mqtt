package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// BuildVersion is overridden at build time using
// -ldflags "-X main.BuildVersion=<version>".
var BuildVersion = "dev"

func main() {

	var dataDir string
	flag.StringVar(&dataDir, "data-dir", "/data/cec2mqtt/", "Sets the directory where the data, including config, files are stored")

	var logLevel string
	flag.StringVar(&logLevel, "log-level", "info", "Sets the log level. Options are error, warning, info, debug, trace")

	var logFormat string
	flag.StringVar(&logFormat, "log-format", "text", "Sets the log format. Options are text, json")

	var logCecMessages bool
	flag.BoolVar(&logCecMessages, "log-cec-messages", false, "Enables logging of the libcec log")

	flag.Parse()

	if err := setupLogging(os.Stderr, logLevel, logFormat); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	slog.Info("Starting Cec2Mqtt", "version", BuildVersion)

	dataDir = strings.TrimRight(dataDir, "/") + "/"

	config, err := ParseConfig(dataDir)

	if nil != err {
		logFatal("Error reading configuration", "error", err)
	}

	devices := NewDeviceRegistry(dataDir)

	mqtt, err := ConnectMqtt(config)

	if nil != err {
		// Not the whole MQTT configuration, it contains the password.
		logFatal("Failed to connect to MQTT broker",
			"host", config.Mqtt.Host,
			"error", err,
		)
	}

	cec, err := InitialiseCec(devices, config.Cec.Adapter)
	if nil != err {
		logFatal("Failed to setup CEC connection", "error", err)
	}

	cec.LibCecLoggingEnabled = logCecMessages

	app := newApp(config, devices, mqtt, cec)

	if err := cec.Start(); err != nil {
		logFatal("Failed to open CEC connection", "error", err)
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("Cec2Mqtt started")
	sig := <-signals
	slog.Info("Shutting down", "signal", sig.String())

	go func() {
		select {
		case <-signals:
			slog.Warn("Received a second signal, exiting immediately")
		case <-time.After(shutdownTimeout):
			slog.Error("Shutdown timed out, exiting", "timeout", shutdownTimeout)
		}
		os.Exit(1)
	}()

	shutdown(app, mqtt, devices)
	slog.Info("Exited")
}

// shutdownTimeout limits how long shutting down may take, so a hanging libcec
// call or MQTT broker can't keep the process (and the CEC adapter) alive.
const shutdownTimeout = 10 * time.Second

// shutdown stops the bridges, publishes the will message and disconnects from
// MQTT, and saves the devices. libcec has no way to close the connection
// through gocec, the adapter is released when the process exits.
func shutdown(app *App, mqtt *Mqtt, devices *DeviceRegistry) {
	app.Close()
	mqtt.Close()

	if err := devices.Save(); err != nil {
		slog.Error("Failed to save devices on shutdown", "error", err)
	}
}
