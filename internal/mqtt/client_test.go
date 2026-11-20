package mqtt

import (
	"slices"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/splattner/cec2mqtt/internal/config"
)

// fakePahoClient implements the parts of paho.Client used by Client.Close.
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
		config    config.Mqtt
		connected bool
		want      []string
	}{
		"state topic": {
			config:    config.Mqtt{StateTopic: "cec2mqtt/state", BirthMessage: "online", WillMessage: "offline"},
			connected: true,
			want:      []string{"publish cec2mqtt/state offline", "disconnect"},
		},
		"no state topic": {
			config:    config.Mqtt{},
			connected: true,
			want:      []string{"disconnect"},
		},
		"not connected": {
			config: config.Mqtt{StateTopic: "cec2mqtt/state", WillMessage: "offline"},
			want:   []string{"disconnect"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakePahoClient{connected: tt.connected}
			m := &Client{client: client, config: &tt.config, subscriptions: make(map[string]subscription)}

			m.Close()

			if !slices.Equal(client.calls, tt.want) {
				t.Errorf("got %q, want %q", client.calls, tt.want)
			}
		})
	}
}
