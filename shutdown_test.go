package main

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/RobertMe/gocec"
	paho "github.com/eclipse/paho.mqtt.golang"
)

type closeRecorder struct {
	name   string
	closed *[]string
}

func (c *closeRecorder) Close() { *c.closed = append(*c.closed, c.name) }

func TestContainerClosesInReverseOrder(t *testing.T) {
	var closed []string
	container := NewContainer()
	container.Register("mqtt", &closeRecorder{"mqtt", &closed})
	container.Register("config", &Config{}) // not a closer
	container.Register("home-assistant", &closeRecorder{"home-assistant", &closed})
	container.Register("bridge.power", &closeRecorder{"bridge.power", &closed})
	container.Register("bridge.removed", &closeRecorder{"bridge.removed", &closed})
	container.Unregister("bridge.removed")
	// Registering again replaces the service, but keeps its position.
	container.Register("mqtt", &closeRecorder{"mqtt (replaced)", &closed})

	container.Close()

	if want := []string{"bridge.power", "home-assistant", "mqtt (replaced)"}; !slices.Equal(closed, want) {
		t.Errorf("closed %q, want %q", closed, want)
	}
}

// fakePahoClient implements the parts of paho.Client used by Mqtt.Close.
type fakePahoClient struct {
	paho.Client
	connected bool
	calls     []string
}

func (c *fakePahoClient) IsConnected() bool { return c.connected }

func (c *fakePahoClient) Publish(topic string, qos byte, retained bool, payload interface{}) paho.Token {
	if !retained {
		topic += " (not retained)"
	}
	c.calls = append(c.calls, "publish "+topic+" "+payload.(string))
	return doneToken{}
}

func (c *fakePahoClient) Disconnect(quiesce uint) {
	c.calls = append(c.calls, "disconnect")
	c.connected = false
}

type doneToken struct{}

func (doneToken) Wait() bool                     { return true }
func (doneToken) WaitTimeout(time.Duration) bool { return true }
func (doneToken) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
func (doneToken) Error() error { return nil }

func TestMqttCloseSendsWillMessage(t *testing.T) {
	tests := map[string]struct {
		config    MqttConfig
		connected bool
		want      []string
	}{
		"state topic": {
			config:    MqttConfig{StateTopic: "cec2mqtt/state", BirthMessage: "online", WillMessage: "offline"},
			connected: true,
			want:      []string{"publish cec2mqtt/state offline", "disconnect"},
		},
		"no state topic": {
			config:    MqttConfig{},
			connected: true,
			want:      []string{"disconnect"},
		},
		"not connected": {
			config: MqttConfig{StateTopic: "cec2mqtt/state", WillMessage: "offline"},
			want:   []string{"disconnect"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakePahoClient{connected: tt.connected}
			m := &Mqtt{client: client, config: &tt.config, subscriptions: make(map[string]subscription)}

			m.Close()

			if !slices.Equal(client.calls, tt.want) {
				t.Errorf("got %q, want %q", client.calls, tt.want)
			}
		})
	}
}

func TestHomeAssistantCloseDropsPendingDiscovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mqtt := newFakeMqtt()
		bridge := newTestHomeAssistantBridge(mqtt)
		bridge.publishDelay = 500 * time.Millisecond
		registry := newTestRegistry(t)
		tv := addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")

		bridge.RegisterSwitch(tv, "power")
		bridge.Close()
		bridge.RegisterSwitch(tv, "power") // registering after Close doesn't publish either
		time.Sleep(time.Second)

		if payload, _ := mqtt.discovery(t, tv.Id); payload != nil {
			t.Errorf("discovery published after Close: %v", payload)
		}
	})
}

func TestBridgesStopPollingOnClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cec := &fakeCec{address: gocec.DeviceRecodingDevice1}
		mqtt := newFakeMqtt()
		registry := newTestRegistry(t)

		audio := newAudioBridge(cec, mqtt)
		registry.RegisterDeviceAddedHandler(audio.deviceAdded)
		power := newPowerBridge(cec, mqtt, registry)
		registry.RegisterDeviceAddedHandler(power.deviceAdded)
		activeSource := newActiveSourceBridge(cec, mqtt, registry)
		activeSource.monitor = CreateMonitor(func() {}, activeSource.checkActiveSource, time.Minute, time.Second, time.Minute)
		registry.RegisterDeviceAddedHandler(activeSource.deviceAdded)

		addTestDevice(t, registry, gocec.DeviceTV, gocec.PhysicalAddress{0, 0}, "TV")
		addTestDevice(t, registry, gocec.DeviceAudiosystem, gocec.PhysicalAddress{0x10, 0}, "Receiver")
		time.Sleep(time.Hour)

		audio.Close()
		power.Close()
		activeSource.Close()

		sent := len(cec.sent())
		cec.mux.Lock()
		activeSourceRequests := cec.activeSourceRequests
		cec.mux.Unlock()

		// Every goroutine of the bridges has ended, otherwise synctest.Test
		// would fail when this function returns.
		time.Sleep(time.Hour)

		if got := len(cec.sent()); got != sent {
			t.Errorf("%d CEC messages sent after Close", got-sent)
		}
		cec.mux.Lock()
		defer cec.mux.Unlock()
		if cec.activeSourceRequests != activeSourceRequests {
			t.Errorf("active source requested after Close")
		}
	})
}
