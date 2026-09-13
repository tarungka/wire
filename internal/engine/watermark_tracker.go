package engine

import (
	"math"
	"sync/atomic"
	"time"
)

// InputWatermarkTracker tracks per-input watermarks and activity for
// multi-input operators. It computes the minimum watermark across all
// non-idle inputs for watermark propagation.
type InputWatermarkTracker struct {
	idleTimeouts   []time.Duration // Optional per-input overrides; immutable after startup.
	ordered        bool            // TaskSlot applies watermark updates on the operator goroutine.
	watermarks     []atomic.Int64  // per-input current watermark (millis)
	lastActivityNs []atomic.Int64  // per-input last activity time (UnixNano)
	pending        []atomic.Int64  // Records read but not yet processed, including alignment buffers.
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
		pending:        make([]atomic.Int64, numInputs),
		numInputs:      numInputs,
		clock:          clock,
	}
	// Every newly connected input gets a full idle timeout before exclusion.
	now := clock()
	for i := range tracker.lastActivityNs {
		tracker.lastActivityNs[i].Store(now)
		tracker.watermarks[i].Store(math.MinInt64)
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

func (t *InputWatermarkTracker) recordQueued(input int) {
	t.pending[input].Add(1)
	t.RecordActivity(input)
}

func (t *InputWatermarkTracker) recordProcessed(input int) {
	// Start the idle timeout after processing catches up, not while a slow
	// operator or checkpoint alignment is holding this input's records.
	t.RecordActivity(input)
	t.pending[input].Add(-1)
}

// MinWatermark returns the minimum watermark across all non-idle inputs.
// An input is considered idle if no activity has been recorded within
// idleTimeout. If all inputs are idle, returns (0, true).
func (t *InputWatermarkTracker) MinWatermark(idleTimeout time.Duration) (int64, bool) {
	now := t.clock()

	var minWM int64
	found := false

	for i := 0; i < t.numInputs; i++ {
		timeout := idleTimeout
		if i < len(t.idleTimeouts) && t.idleTimeouts[i] != 0 {
			timeout = t.idleTimeouts[i]
		}
		lastActivity := t.lastActivityNs[i].Load()
		if t.pending[i].Load() == 0 && timeout > 0 && (now-lastActivity) >= timeout.Nanoseconds() {
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
