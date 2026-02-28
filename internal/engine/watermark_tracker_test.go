package engine

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInputWatermarkTracker_MinSelection(t *testing.T) {
	tracker := NewInputWatermarkTracker(3)
	// Disable idle detection for this test by setting activity on all inputs.
	tracker.clock = func() int64 { return 1000 }
	for i := 0; i < 3; i++ {
		tracker.RecordActivity(i)
	}

	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)
	tracker.AdvanceWatermark(2, 50)

	minWM, allIdle := tracker.MinWatermark(time.Minute)
	if allIdle {
		t.Fatal("expected not all idle")
	}
	if minWM != 50 {
		t.Errorf("min watermark: got %d, want 50", minWM)
	}
}

func TestInputWatermarkTracker_AdvanceDoesNotRegress(t *testing.T) {
	tracker := NewInputWatermarkTracker(1)

	tracker.AdvanceWatermark(0, 200)
	tracker.AdvanceWatermark(0, 100) // Stale — should not regress.

	if wm := tracker.watermarks[0].Load(); wm != 200 {
		t.Errorf("watermark regressed: got %d, want 200", wm)
	}
}

func TestInputWatermarkTracker_IdleExclusion(t *testing.T) {
	var now atomic.Int64
	now.Store(1000)

	tracker := NewInputWatermarkTracker(3)
	tracker.clock = func() int64 { return now.Load() }

	// Record activity on all inputs at time 1000.
	for i := 0; i < 3; i++ {
		tracker.RecordActivity(i)
	}
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)
	tracker.AdvanceWatermark(2, 50)

	// All active — min should be 50.
	minWM, allIdle := tracker.MinWatermark(time.Minute)
	if allIdle || minWM != 50 {
		t.Errorf("expected min=50, allIdle=false; got min=%d, allIdle=%t", minWM, allIdle)
	}

	// Advance time so input 2 becomes idle (> 1 minute).
	// Input 2 last activity at 1000ns, now at 1000 + 61s in nanos.
	now.Store(1000 + int64(61*time.Second))
	// Re-record activity on inputs 0 and 1 so they stay active.
	tracker.RecordActivity(0)
	tracker.RecordActivity(1)

	// Input 2 is idle — min should be 100 (from inputs 0 and 1).
	minWM, allIdle = tracker.MinWatermark(time.Minute)
	if allIdle {
		t.Fatal("expected not all idle")
	}
	if minWM != 100 {
		t.Errorf("min watermark with idle input: got %d, want 100", minWM)
	}
}

func TestInputWatermarkTracker_AllIdle(t *testing.T) {
	var now atomic.Int64
	now.Store(1000)

	tracker := NewInputWatermarkTracker(2)
	tracker.clock = func() int64 { return now.Load() }

	// Record activity at time 1000.
	tracker.RecordActivity(0)
	tracker.RecordActivity(1)
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)

	// Advance time past idle timeout.
	now.Store(1000 + int64(2*time.Minute))

	_, allIdle := tracker.MinWatermark(time.Minute)
	if !allIdle {
		t.Error("expected all idle")
	}
}

func TestInputWatermarkTracker_ReActivation(t *testing.T) {
	var now atomic.Int64
	now.Store(1000)

	tracker := NewInputWatermarkTracker(2)
	tracker.clock = func() int64 { return now.Load() }

	// Record initial activity.
	tracker.RecordActivity(0)
	tracker.RecordActivity(1)
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)

	// Make input 1 idle.
	now.Store(1000 + int64(2*time.Minute))
	tracker.RecordActivity(0) // Keep 0 active.

	minWM, allIdle := tracker.MinWatermark(time.Minute)
	if allIdle || minWM != 100 {
		t.Errorf("expected min=100 with input 1 idle: got min=%d, allIdle=%t", minWM, allIdle)
	}

	// Re-activate input 1.
	tracker.RecordActivity(1)
	tracker.AdvanceWatermark(1, 50) // Won't advance since 50 < 200.

	minWM, allIdle = tracker.MinWatermark(time.Minute)
	if allIdle {
		t.Fatal("expected not all idle after re-activation")
	}
	// Input 1 watermark is still 200, input 0 is 100 — min is 100.
	if minWM != 100 {
		t.Errorf("min watermark after re-activation: got %d, want 100", minWM)
	}
}

func TestInputWatermarkTracker_NoIdleTimeout(t *testing.T) {
	// With idleTimeout=0, all inputs participate regardless of activity.
	tracker := NewInputWatermarkTracker(2)
	// Don't record any activity.
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)

	minWM, allIdle := tracker.MinWatermark(0)
	if allIdle {
		t.Fatal("expected not all idle with zero timeout")
	}
	if minWM != 100 {
		t.Errorf("min watermark: got %d, want 100", minWM)
	}
}

func TestInputWatermarkTracker_NeverSeenActivity(t *testing.T) {
	// Inputs with no activity and a configured timeout are treated as idle.
	tracker := NewInputWatermarkTracker(2)
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)

	_, allIdle := tracker.MinWatermark(time.Minute)
	if !allIdle {
		t.Error("expected all idle when no activity recorded")
	}
}

func TestInputWatermarkTracker_ConcurrentCAS(t *testing.T) {
	tracker := NewInputWatermarkTracker(1)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for j := int64(0); j < 100; j++ {
				tracker.AdvanceWatermark(0, base+j)
			}
		}(int64(i * 1000))
	}
	wg.Wait()

	// Should have advanced to at least 9099.
	if wm := tracker.watermarks[0].Load(); wm < 9099 {
		t.Errorf("concurrent watermark too low: got %d, want >= 9099", wm)
	}
}

func TestInputWatermarkTracker_ConcurrentStress(t *testing.T) {
	// Stress test: concurrent RecordActivity + AdvanceWatermark + MinWatermark
	// across multiple inputs. Designed to be run with -race.
	const numInputs = 4
	tracker := NewInputWatermarkTracker(numInputs)

	var wg sync.WaitGroup

	// Goroutines advancing watermarks on each input.
	for i := 0; i < numInputs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := int64(1); j <= 500; j++ {
				tracker.AdvanceWatermark(idx, j*int64(idx+1))
			}
		}(i)
	}

	// Goroutines recording activity on each input.
	for i := 0; i < numInputs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				tracker.RecordActivity(idx)
			}
		}(i)
	}

	// Goroutines reading MinWatermark concurrently.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_, _ = tracker.MinWatermark(time.Minute)
			}
		}()
	}

	wg.Wait()

	// Verify final state is consistent: each input's watermark should be
	// at its maximum written value.
	for i := 0; i < numInputs; i++ {
		expected := int64(500 * (i + 1))
		if wm := tracker.watermarks[i].Load(); wm != expected {
			t.Errorf("input %d watermark: got %d, want %d", i, wm, expected)
		}
	}
}
