package engine

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// BarrierAligner implements Chandy-Lamport style barrier alignment across
// multiple input channels. When a checkpoint barrier arrives on one input,
// data events from that input are side-buffered until barriers arrive on all
// inputs. Once aligned, buffered events are drained in input order.
type BarrierAligner struct {
	mu             sync.Mutex
	numInputs      int
	maxBufferSize  int
	activeID       uint64          // 0 = no active alignment.
	activeEpoch    uint64          // Epoch of the active checkpoint.
	arrived        map[int]bool    // Which inputs have reported the barrier.
	sideBuffers    map[int][]Event // Per-input side buffers, lazily allocated.
	alignStartTime time.Time       // When alignment started (first barrier arrived).
}

// NewBarrierAligner creates a new BarrierAligner for the given number of inputs.
func NewBarrierAligner(numInputs, maxBufferSize int) *BarrierAligner {
	return &BarrierAligner{
		numInputs:     numInputs,
		maxBufferSize: maxBufferSize,
		arrived:       make(map[int]bool),
		sideBuffers:   make(map[int][]Event),
	}
}

// OnBarrier records that a checkpoint barrier arrived on the given input.
// Returns true if this barrier triggered full alignment (all inputs arrived).
func (ba *BarrierAligner) OnBarrier(inputIndex int, checkpointID, epochID uint64) bool {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	if ba.activeID == 0 {
		// First barrier for this checkpoint — start alignment.
		ba.activeID = checkpointID
		ba.activeEpoch = epochID
		ba.alignStartTime = time.Now()
	}

	ba.arrived[inputIndex] = true

	// Lazily allocate side buffer for this input.
	if _, ok := ba.sideBuffers[inputIndex]; !ok {
		ba.sideBuffers[inputIndex] = make([]Event, 0, ba.maxBufferSize)
	}

	return len(ba.arrived) >= ba.numInputs
}

// IsAligning returns true if the given input has already reported its barrier
// for the active checkpoint, meaning data events from it should be side-buffered.
func (ba *BarrierAligner) IsAligning(inputIndex int) bool {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.activeID != 0 && ba.arrived[inputIndex]
}

// BufferEvent appends an event to the side buffer for the given input.
// Returns ErrSideBufferFull if the buffer is at capacity.
// The caller should use ctx for cancellation-aware retry.
func (ba *BarrierAligner) BufferEvent(ctx context.Context, inputIndex int, event Event) error {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	buf := ba.sideBuffers[inputIndex]
	if len(buf) >= ba.maxBufferSize {
		return fmt.Errorf("%w: input %d has %d events", ErrSideBufferFull, inputIndex, len(buf))
	}

	ba.sideBuffers[inputIndex] = append(buf, event)
	return nil
}

// AllAligned returns true if all inputs have reported the barrier for the
// given checkpoint ID.
func (ba *BarrierAligner) AllAligned(checkpointID uint64) bool {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.activeID == checkpointID && len(ba.arrived) >= ba.numInputs
}

// DrainAll returns all side-buffered events in input order (0, 1, 2, ...),
// then resets buffer lengths while keeping allocated capacity.
func (ba *BarrierAligner) DrainAll(checkpointID uint64) []Event {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	if ba.activeID != checkpointID {
		return nil
	}

	var all []Event
	for i := 0; i < ba.numInputs; i++ {
		buf := ba.sideBuffers[i]
		all = append(all, buf...)
		// Reset length, keep capacity.
		ba.sideBuffers[i] = buf[:0]
	}

	return all
}

// Reset clears alignment state for the given checkpoint, keeping allocated
// buffer capacity for reuse.
func (ba *BarrierAligner) Reset(checkpointID uint64) {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	if ba.activeID != checkpointID && ba.activeID != 0 {
		return
	}

	ba.activeID = 0
	ba.activeEpoch = 0
	ba.alignStartTime = time.Time{}
	ba.arrived = make(map[int]bool)
	// Keep sideBuffers allocated but empty.
	for i := range ba.sideBuffers {
		ba.sideBuffers[i] = ba.sideBuffers[i][:0]
	}
}

// ActiveCheckpointID returns the currently active checkpoint ID (0 if none).
func (ba *BarrierAligner) ActiveCheckpointID() uint64 {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.activeID
}

// ActiveEpochID returns the epoch ID of the currently active checkpoint.
func (ba *BarrierAligner) ActiveEpochID() uint64 {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.activeEpoch
}

// AlignmentStartTime returns the time when alignment started (first barrier arrived).
// Returns zero time if no alignment is active.
func (ba *BarrierAligner) AlignmentStartTime() time.Time {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	return ba.alignStartTime
}

// BufferedEventCount returns the total number of events across all side buffers.
func (ba *BarrierAligner) BufferedEventCount() int {
	ba.mu.Lock()
	defer ba.mu.Unlock()
	var count int
	for _, buf := range ba.sideBuffers {
		count += len(buf)
	}
	return count
}
