package bridge

import "sync"

type queuedMessage struct {
	topic    string
	qos      byte
	retained bool
	payload  interface{}
}

// publishQueue publishes messages one after the other, in the order they were
// queued, without blocking the caller on the network. Publishing on the paho
// client can block for up to its write timeout, which must not hold up libcec
// callbacks. Starting a goroutine per message instead doesn't keep the order,
// so a quick on → off could end up as "on" on MQTT.
type publishQueue struct {
	mqtt     MQTT
	queue    chan queuedMessage
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func newPublishQueue(mqtt MQTT, size int) *publishQueue {
	queue := &publishQueue{
		mqtt:  mqtt,
		queue: make(chan queuedMessage, size),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}

	go queue.run()

	return queue
}

// Publish queues the message. It only blocks when the queue is full. After
// Close the message is dropped.
func (queue *publishQueue) Publish(topic string, qos byte, retained bool, payload interface{}) {
	select {
	case queue.queue <- queuedMessage{topic: topic, qos: qos, retained: retained, payload: payload}:
	case <-queue.stop:
	}
}

// Close stops publishing, dropping messages which are still queued, and waits
// until a running publish returned. It's safe to call more than once.
func (queue *publishQueue) Close() {
	queue.stopOnce.Do(func() { close(queue.stop) })
	<-queue.done
}

func (queue *publishQueue) run() {
	defer close(queue.done)

	for {
		select {
		case <-queue.stop:
			return
		case message := <-queue.queue:
			queue.mqtt.Publish(message.topic, message.qos, message.retained, message.payload)
		}
	}
}
