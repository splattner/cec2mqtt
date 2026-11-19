package main

import "time"

type Starter func()
type Runner func()

type Monitor struct {
	reset chan int

	longInterval  time.Duration
	shortInterval time.Duration
	shortTimer    *time.Timer
	shortDuration time.Duration

	starter Starter
	runner  Runner
}

func CreateMonitor(starter Starter, runner Runner, longInterval time.Duration, shortInterval time.Duration, shortDuration time.Duration) *Monitor {
	monitor := &Monitor{
		reset:         make(chan int, 1),
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

func (monitor *Monitor) run() {
	monitor.starter()
	monitor.runner()

	ticker := time.NewTicker(monitor.shortInterval)
	monitor.shortTimer = time.NewTimer(monitor.shortDuration)

	for {
		select {
		case <-ticker.C:
			monitor.runner()
		case <-monitor.reset:
			ticker.Stop()
			monitor.shortTimer.Stop()

			monitor.starter()
			monitor.runner()

			ticker = time.NewTicker(monitor.shortInterval)
			monitor.shortTimer = time.NewTimer(monitor.shortDuration)

		case <-monitor.shortTimer.C:
			ticker.Stop()
			monitor.shortTimer.Stop()

			ticker = time.NewTicker(monitor.longInterval)
		}
	}
}
