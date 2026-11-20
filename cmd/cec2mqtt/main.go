// Command cec2mqtt bridges HDMI-CEC devices to MQTT, with optional Home
// Assistant discovery.
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

	"github.com/splattner/cec2mqtt/internal/bridge"
	"github.com/splattner/cec2mqtt/internal/cec"
	"github.com/splattner/cec2mqtt/internal/config"
	"github.com/splattner/cec2mqtt/internal/device"
	"github.com/splattner/cec2mqtt/internal/logging"
	"github.com/splattner/cec2mqtt/internal/mqtt"
	"github.com/splattner/cec2mqtt/internal/version"
)

// shutdownTimeout limits how long shutting down may take, so a hanging libcec
// call or MQTT broker can't keep the process (and the CEC adapter) alive.
const shutdownTimeout = 10 * time.Second

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

	if err := logging.Setup(os.Stderr, logLevel, logFormat); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	slog.Info("Starting Cec2Mqtt", "version", version.Version)

	dataDir = strings.TrimRight(dataDir, "/") + "/"

	cfg, err := config.Load(dataDir)
	if err != nil {
		fatal("Error reading configuration", "error", err)
	}

	devices := device.NewRegistry(dataDir)

	mqttClient, err := mqtt.Connect(cfg)
	if err != nil {
		// Not the whole MQTT configuration, it contains the password.
		fatal("Failed to connect to MQTT broker",
			"host", cfg.Mqtt.Host,
			"error", err,
		)
	}

	cecConnection, err := cec.New(devices, cfg.Cec.Adapter)
	if err != nil {
		fatal("Failed to setup CEC connection", "error", err)
	}
	cecConnection.LibCecLoggingEnabled = logCecMessages

	bridges := bridge.New(cfg, devices, mqttClient, cecConnection)

	if err := cecConnection.Start(); err != nil {
		cecConnection.Close()
		fatal("Failed to open CEC connection", "error", err)
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

	// Bridges first, so nothing is sent while the connections close. MQTT
	// before CEC, so Home Assistant shows the devices as unavailable soon.
	bridges.Close()
	mqttClient.Close()
	cecConnection.Close()

	if err := devices.Save(); err != nil {
		slog.Error("Failed to save devices on shutdown", "error", err)
	}

	slog.Info("Exited")
}

// fatal logs a message at error level and exits the process.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
