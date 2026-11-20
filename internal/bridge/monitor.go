package bridge

import (
	"sync"
	"time"
)

type Starter func()
type Runner func()

type Monitor struct {
	reset chan int

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	longInterval  time.Duration
	shortInterval time.Duration
	shortDuration time.Duration

	starter Starter
	runner  Runner
}

func CreateMonitor(starter Starter, runner Runner, longInterval time.Duration, shortInterval time.Duration, shortDuration time.Duration) *Monitor {
	monitor := &Monitor{
		reset:         make(chan int, 1),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		longInterval:  longInterval,
		shortInterval: shortInterval,
		shortDuration: shortDuration,
		starter:       starter,
		runner:        runner,
	}

	go monitor.run()

	return monitor
}

// Reset restarts the monitor with the short interval. It never blocks: when a
// reset is already pending, the new request is merged into it.
func (monitor *Monitor) Reset() {
	select {
	case monitor.reset <- 0:
	default:
	}
}

// Stop stops the monitor and waits until a running starter or runner returned.
// It's safe to call more than once.
func (monitor *Monitor) Stop() {
	monitor.stopOnce.Do(func() { close(monitor.stop) })
	<-monitor.done
}

func (monitor *Monitor) run() {
	defer close(monitor.done)

	monitor.starter()
	monitor.runner()

	ticker := time.NewTicker(monitor.shortInterval)
	shortTimer := time.NewTimer(monitor.shortDuration)
	defer func() {
		ticker.Stop()
		shortTimer.Stop()
	}()

	for {
		select {
		case <-monitor.stop:
			return
		case <-ticker.C:
			monitor.runner()
		case <-monitor.reset:
			monitor.starter()
			monitor.runner()

			ticker.Reset(monitor.shortInterval)
			shortTimer.Reset(monitor.shortDuration)

		case <-shortTimer.C:
			ticker.Reset(monitor.longInterval)
		}
	}
}
