package bridge

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestMonitorRunsAndResets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var starts, runs atomic.Int32

		monitor := CreateMonitor(
			func() { starts.Add(1) },
			func() { runs.Add(1) },
			time.Hour,   // long interval
			time.Second, // short interval
			time.Hour,   // short duration
		)
		defer monitor.Stop()

		synctest.Wait()
		if starts.Load() != 1 || runs.Load() != 1 {
			t.Fatalf("after start: starts = %d, runs = %d, want 1, 1", starts.Load(), runs.Load())
		}

		time.Sleep(3500 * time.Millisecond)
		if runs.Load() != 4 {
			t.Errorf("after 3.5s: runs = %d, want 4", runs.Load())
		}

		monitor.Reset()
		synctest.Wait()
		if starts.Load() != 2 || runs.Load() != 5 {
			t.Errorf("after reset: starts = %d, runs = %d, want 2, 5", starts.Load(), runs.Load())
		}

		// The reset restarted the short interval.
		time.Sleep(1500 * time.Millisecond)
		if runs.Load() != 6 {
			t.Errorf("1.5s after reset: runs = %d, want 6", runs.Load())
		}
	})
}

func TestMonitorSwitchesToLongInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32

		monitor := CreateMonitor(
			func() {},
			func() { runs.Add(1) },
			time.Hour,
			time.Second,
			10500*time.Millisecond,
		)
		defer monitor.Stop()

		// The initial run plus one every second during the short duration.
		time.Sleep(11 * time.Second)
		if runs.Load() != 11 {
			t.Errorf("during short duration: runs = %d, want 11", runs.Load())
		}

		time.Sleep(59 * time.Minute)
		if runs.Load() != 11 {
			t.Errorf("runner still called with short interval after short duration ended: runs = %d", runs.Load())
		}

		time.Sleep(time.Minute)
		if runs.Load() != 12 {
			t.Errorf("after long interval: runs = %d, want 12", runs.Load())
		}
	})
}

func TestMonitorResetDoesNotBlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})

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

		for range 100 {
			monitor.Reset()
		}

		// All resets were merged into one.
		close(release)
		synctest.Wait()
		if starts.Load() != 2 {
			t.Errorf("starts = %d, want 2", starts.Load())
		}

		monitor.Stop()
	})
}

func TestMonitorStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var runs atomic.Int32
		monitor := CreateMonitor(func() {}, func() { runs.Add(1) }, time.Second, time.Second, time.Second)

		time.Sleep(2500 * time.Millisecond)
		monitor.Stop()
		monitor.Stop()  // safe to call again
		monitor.Reset() // doesn't block after stopping

		after := runs.Load()
		time.Sleep(time.Hour)
		if runs.Load() != after {
			t.Errorf("runner called after Stop: %d -> %d", after, runs.Load())
		}
	})
}
