package main

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/splattner/cec2mqtt/internal/gocec"
)

// These tests run in a synctest bubble: time is fake and only advances when
// every goroutine is blocked, so the polling intervals can be tested exactly
// and without waiting.

const tvPowerTopic = "cec2mqtt/TV/power"

func newTestPowerBridge(t *testing.T) (*PowerBridge, *fakeCec, *fakeMqtt, *DeviceRegistry) {
	t.Helper()
	cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
	mqtt := newFakeMqtt()
	registry := newTestRegistry(t)
	bridge := newPowerBridge(cec, mqtt, registry)
	registry.RegisterDeviceAddedHandler(bridge.deviceAdded)
	return bridge, cec, mqtt, registry
}

func reportPowerStatus(source gocec.LogicalAddress, status gocec.PowerStatus) gocec.Message {
	return gocec.NewMessage(source, gocec.DeviceRecodingDevice1, gocec.OpcodeReportPowerStatus, []byte{byte(status)})
}

func powerRequests(cec *fakeCec) int {
	return len(cec.sent(gocec.OpcodeGiveDevicePowerStatus))
}

func TestPowerBridgePublishesStatesInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, mqtt, registry := newTestPowerBridge(t)
		defer bridge.Close()

		cec.setPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn)
		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		synctest.Wait()

		want := []string{"on"}
		// Quick changes, like a TV going into standby and being woken again.
		// Earlier versions published every state from its own goroutine, so
		// these could arrive on MQTT in any order.
		for i := 0; i < 50; i++ {
			bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusStandBy))
			bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
			want = append(want, "off", "on")
		}
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusStandBy))
		want = append(want, "off")
		synctest.Wait()

		if got := mqtt.messages(tvPowerTopic); !slices.Equal(got, want) {
			t.Errorf("published power states out of order:\n got %q\nwant %q", got, want)
		}
	})
}

func TestPowerBridgeOnlyPublishesChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, _, mqtt, registry := newTestPowerBridge(t)
		defer bridge.Close()

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		synctest.Wait()

		// Transitions are reported as the state the device is going to.
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusTransitionToOn))
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
		synctest.Wait()

		// The initial state is unknown, as libcec doesn't know it either.
		if got, want := mqtt.messages(tvPowerTopic), []string{"unknown", "on"}; !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}

		// When the device stops answering, the last state is kept on MQTT...
		bridge.MonitorPower(registry.FindByLogicalAddress(gocec.DeviceTV).Id)
		synctest.Wait()
		if got, want := mqtt.messages(tvPowerTopic), []string{"unknown", "on"}; !slices.Equal(got, want) {
			t.Errorf("unknown status after a known one: got %q, want %q", got, want)
		}

		// ... and published again once it's known again.
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
		synctest.Wait()
		if got, want := mqtt.messages(tvPowerTopic), []string{"unknown", "on", "on"}; !slices.Equal(got, want) {
			t.Errorf("known again: got %q, want %q", got, want)
		}

		// Reports from devices which aren't known are ignored.
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DevicePlaybackDevice1, gocec.PowerStatusOn))
		bridge.handleReportPowerStatus(gocec.NewMessage(gocec.DeviceTV, gocec.DeviceRecodingDevice1, gocec.OpcodeReportPowerStatus, nil))
		synctest.Wait()
		if got := mqtt.messages(tvPowerTopic); len(got) != 3 {
			t.Errorf("unexpected messages: %q", got)
		}
	})
}

func TestPowerBridgeCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, mqtt, registry := newTestPowerBridge(t)
		defer bridge.Close()

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		player := addTestDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")
		synctest.Wait()

		mqtt.deliver(t, "cec2mqtt/TV/power/set", "off")
		mqtt.deliver(t, "cec2mqtt/Player/power/set", "on")
		mqtt.deliver(t, "cec2mqtt/Player/power/set", "toggle")
		synctest.Wait()

		cec.mux.Lock()
		defer cec.mux.Unlock()
		if want := []gocec.LogicalAddress{gocec.DeviceTV}; !slices.Equal(cec.standby, want) {
			t.Errorf("standby sent to %v, want %v", cec.standby, want)
		}
		if want := []gocec.LogicalAddress{player.LogicalAddress}; !slices.Equal(cec.poweredOn, want) {
			t.Errorf("power on sent to %v, want %v", cec.poweredOn, want)
		}
	})
}

func TestPowerBridgePollsMoreOftenAfterCommand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, mqtt, registry := newTestPowerBridge(t)
		defer bridge.Close()

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		synctest.Wait()

		// The requests from starting the monitor and its first run are merged.
		if got := powerRequests(cec); got != 1 {
			t.Errorf("requests after adding the device = %d, want 1", got)
		}

		// Settle into the long interval.
		time.Sleep(time.Hour)
		idle := powerRequests(cec)
		time.Sleep(4 * time.Minute)
		if got := powerRequests(cec) - idle; got > 1 {
			t.Errorf("%d requests in 4 minutes while idle, want at most 1", got)
		}

		before := powerRequests(cec)
		mqtt.deliver(t, "cec2mqtt/TV/power/set", "off")
		synctest.Wait()
		if got := powerRequests(cec) - before; got != 1 {
			t.Errorf("requests right after the command = %d, want 1", got)
		}

		// Polled every 2 seconds for 30 seconds...
		time.Sleep(11 * time.Second)
		if got := powerRequests(cec) - before; got != 6 {
			t.Errorf("requests 11s after the command = %d, want 6", got)
		}

		// ... and then every 5 minutes again.
		time.Sleep(time.Minute)
		shortPhase := powerRequests(cec)
		time.Sleep(3 * time.Minute)
		if got := powerRequests(cec) - shortPhase; got != 0 {
			t.Errorf("%d requests after the short interval ended, want 0", got)
		}

		assertMessages(t, cec.sent(gocec.OpcodeGiveDevicePowerStatus)[:1], []gocec.Message{{0x10, byte(gocec.OpcodeGiveDevicePowerStatus)}})
	})
}

func TestPowerBridgePollsOnDeviceActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, _, registry := newTestPowerBridge(t)
		defer bridge.Close()

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		addTestDevice(t, registry, gocec.DevicePlaybackDevice1, gocec.PhysicalAddress{0x10, 0}, "Player")
		time.Sleep(time.Hour)

		requestsTo := func(address gocec.LogicalAddress) int {
			count := 0
			for _, message := range cec.sent(gocec.OpcodeGiveDevicePowerStatus) {
				if message.Destination() == address {
					count++
				}
			}
			return count
		}

		// A TV woken by its own remote announces itself.
		tv, player := requestsTo(gocec.DeviceTV), requestsTo(gocec.DevicePlaybackDevice1)
		bridge.handleDeviceActivity(gocec.NewMessage(gocec.DeviceTV, gocec.DeviceBroadcast, gocec.OpcodeReportPhysicalAddress, []byte{0, 0, 0}))
		synctest.Wait()
		if requestsTo(gocec.DeviceTV) != tv+1 || requestsTo(gocec.DevicePlaybackDevice1) != player {
			t.Errorf("expected only the TV to be polled after its announcement")
		}

		// Unknown devices are ignored.
		bridge.handleDeviceActivity(gocec.NewMessage(gocec.DeviceAudiosystem, gocec.DeviceBroadcast, gocec.OpcodeReportPhysicalAddress, []byte{0x20, 0, 5}))

		// A broadcast standby polls all devices.
		time.Sleep(time.Hour)
		tv, player = requestsTo(gocec.DeviceTV), requestsTo(gocec.DevicePlaybackDevice1)
		bridge.handleBusChange(gocec.NewMessage(gocec.DeviceTV, gocec.DeviceBroadcast, gocec.OpcodeStandby, nil))
		synctest.Wait()
		if requestsTo(gocec.DeviceTV) != tv+1 || requestsTo(gocec.DevicePlaybackDevice1) != player+1 {
			t.Errorf("expected all devices to be polled after a broadcast standby")
		}
	})
}

func TestPowerBridgeResendAll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, mqtt, registry := newTestPowerBridge(t)
		defer bridge.Close()
		bridge.haBridge = newTestHomeAssistantBridge(mqtt)

		cec.setPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn)
		tv := addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		synctest.Wait()

		mqtt.reset()
		bridge.resendAll()
		synctest.Wait()

		if got, want := mqtt.messages(tvPowerTopic), []string{"on"}; !slices.Equal(got, want) {
			t.Errorf("resent power states: got %q, want %q", got, want)
		}
		if _, components := mqtt.discovery(t, tv.Id); components["power"]["platform"] != "switch" {
			t.Errorf("expected the power switch to be rediscovered, got %v", components)
		}
	})
}

func TestPowerBridgeClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		bridge, cec, mqtt, registry := newTestPowerBridge(t)

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		synctest.Wait()
		bridge.Close()

		requests := powerRequests(cec)
		messages := len(mqtt.messages(tvPowerTopic))

		// Late libcec callbacks neither block nor publish.
		bridge.handleReportPowerStatus(reportPowerStatus(gocec.DeviceTV, gocec.PowerStatusOn))
		time.Sleep(time.Hour)

		if powerRequests(cec) != requests {
			t.Errorf("still polling after Close")
		}
		if len(mqtt.messages(tvPowerTopic)) != messages {
			t.Errorf("still publishing after Close")
		}
	})
}
