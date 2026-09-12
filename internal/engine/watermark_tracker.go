package engine

import (
	"sync/atomic"
	"time"
)

// InputWatermarkTracker tracks per-input watermarks and activity for
// multi-input operators. It computes the minimum watermark across all
// non-idle inputs for watermark propagation.
type InputWatermarkTracker struct {
	watermarks     []atomic.Int64 // per-input current watermark (millis)
	lastActivityNs []atomic.Int64 // per-input last activity time (UnixNano)
	numInputs      int
	clock          func() int64 // returns UnixNano; injectable for testing
}

// NewInputWatermarkTracker creates a tracker for the given number of inputs.
func NewInputWatermarkTracker(numInputs int) *InputWatermarkTracker {
	return newInputWatermarkTracker(numInputs, func() int64 { return time.Now().UnixNano() })
}

func newInputWatermarkTracker(numInputs int, clock func() int64) *InputWatermarkTracker {
	tracker := &InputWatermarkTracker{
		watermarks:     make([]atomic.Int64, numInputs),
		lastActivityNs: make([]atomic.Int64, numInputs),
		numInputs:      numInputs,
		clock:          clock,
	}
	// Every newly connected input gets a full idle timeout before exclusion.
	now := clock()
	for i := range tracker.lastActivityNs {
		tracker.lastActivityNs[i].Store(now)
	}
	return tracker
}

// AdvanceWatermark CAS-advances the watermark for the given input.
// The watermark never regresses.
func (t *InputWatermarkTracker) AdvanceWatermark(inputIndex int, timestamp int64) {
	wm := &t.watermarks[inputIndex]
	for {
		cur := wm.Load()
		if timestamp <= cur {
			return
		}
		if wm.CompareAndSwap(cur, timestamp) {
			return
		}
	}
}

// RecordActivity records that the given input has seen activity (a data record).
func (t *InputWatermarkTracker) RecordActivity(inputIndex int) {
	t.lastActivityNs[inputIndex].Store(t.clock())
}

// MinWatermark returns the minimum watermark across all non-idle inputs.
// An input is considered idle if no activity has been recorded within
// idleTimeout. If all inputs are idle, returns (0, true).
func (t *InputWatermarkTracker) MinWatermark(idleTimeout time.Duration) (int64, bool) {
	now := t.clock()
	idleThresholdNs := idleTimeout.Nanoseconds()

	var minWM int64
	found := false

	for i := 0; i < t.numInputs; i++ {
		lastActivity := t.lastActivityNs[i].Load()
		if idleTimeout > 0 && (now-lastActivity) >= idleThresholdNs {
			continue // idle, skip
		}

		wm := t.watermarks[i].Load()
		if !found || wm < minWM {
			minWM = wm
			found = true
		}
	}

	if !found {
		return 0, true // all idle
	}
	return minWM, false
}
