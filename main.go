package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
)

// BuildVersion is overridden at build time using
// -ldflags "-X main.BuildVersion=<version>".
var BuildVersion = "dev"

type Initializer func(container *Container)

var initializers = make(map[int][]Initializer, 0)

func RegisterInitializer(priority int, initializer Initializer) {
	if _, ok := initializers[priority]; ok {
		initializers[priority] = append(initializers[priority], initializer)
	} else {
		initializers[priority] = []Initializer{initializer}
	}
}

func runInitializers(container *Container) {
	priorities := make([]int, len(initializers))
	i := 0
	for priority := range initializers {
		priorities[i] = priority
		i++
	}

	sort.Ints(priorities)
	for i := len(priorities) - 1; i >= 0; i-- {
		for _, initializer := range initializers[priorities[i]] {
			initializer(container)
		}
	}
}

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

	container := NewContainer()

	config, err := ParseConfig(dataDir)

	if nil != err {
		logFatal("Error reading configuration", "error", err)
	}

	container.Register("config", config)

	devices := NewDeviceRegistry(dataDir)
	container.Register("devices", devices)

	mqtt, err := ConnectMqtt(config)

	if nil != err {
		logFatal("Failed to connect to MQTT broker",
			"config", config.Mqtt,
			"error", err,
		)
	}

	container.Register("mqtt", mqtt)

	cec, err := InitialiseCec(devices, config.Cec.Adapter)
	if nil != err {
		logFatal("Failed to setup CEC connection", "error", err)
	}

	cec.LibCecLoggingEnabled = logCecMessages

	container.Register("cec", cec)

	runInitializers(container)

	if err := cec.Start(); err != nil {
		logFatal("Failed to open CEC connection", "error", err)
	}

	signals := make(chan os.Signal, 1)
	done := make(chan bool, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signals
		done <- true
	}()

	slog.Info("Cec2Mqtt started")
	<-done
	slog.Info("Exiting")
	devices.Save()
}
