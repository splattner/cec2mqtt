package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestMonitorRunsAndResets(t *testing.T) {
	var starts, runs atomic.Int32

	monitor := CreateMonitor(
		func() { starts.Add(1) },
		func() { runs.Add(1) },
		time.Hour,           // long interval: never reached in this test
		10*time.Millisecond, // short interval
		time.Hour,           // short duration
	)

	waitFor(t, "initial start", func() bool { return starts.Load() == 1 })
	waitFor(t, "periodic runs", func() bool { return runs.Load() >= 3 })

	monitor.Reset()
	waitFor(t, "start after reset", func() bool { return starts.Load() == 2 })
}

func TestMonitorSwitchesToLongInterval(t *testing.T) {
	var runs atomic.Int32

	CreateMonitor(
		func() {},
		func() { runs.Add(1) },
		time.Hour,
		5*time.Millisecond,
		50*time.Millisecond,
	)

	time.Sleep(150 * time.Millisecond)
	after := runs.Load()
	time.Sleep(100 * time.Millisecond)

	if runs.Load() != after {
		t.Errorf("runner still called with short interval after short duration ended (%d -> %d)", after, runs.Load())
	}
}

func TestMonitorResetDoesNotBlock(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	var starts atomic.Int32
	monitor := CreateMonitor(
		func() {
			// Block the first start so the monitor can't receive any resets.
			if starts.Add(1) == 1 {
				<-release
			}
		},
		func() {},
		time.Hour,
		time.Hour,
		time.Hour,
	)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			monitor.Reset()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Reset blocked while the monitor was busy")
	}
}
