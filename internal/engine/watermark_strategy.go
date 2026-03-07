package engine

import (
	"sync/atomic"
	"time"
)

// WatermarkStrategy generates watermark timestamps for source tasks.
// Implementations must be safe for concurrent use.
type WatermarkStrategy interface {
	// GenerateWatermark returns the current watermark timestamp (millis).
	GenerateWatermark() int64
	// ObserveEventTime records an event timestamp for watermark computation.
	ObserveEventTime(eventTime int64)
}

// BoundedOutOfOrdernessStrategy allows events to arrive out of order up to
// a configurable bound. The watermark is max(0, maxObserved - maxOOO).
type BoundedOutOfOrdernessStrategy struct {
	maxObservedTimestamp atomic.Int64
	maxOutOfOrderness    int64 // millis
}

// NewBoundedOutOfOrdernessStrategy creates a strategy with the given maximum
// out-of-orderness. If maxOOO <= 0, DefaultMaxOOO is used.
func NewBoundedOutOfOrdernessStrategy(maxOOO time.Duration) *BoundedOutOfOrdernessStrategy {
	if maxOOO <= 0 {
		maxOOO = DefaultMaxOOO
	}
	return &BoundedOutOfOrdernessStrategy{
		maxOutOfOrderness: maxOOO.Milliseconds(),
	}
}

func (s *BoundedOutOfOrdernessStrategy) GenerateWatermark() int64 {
	wm := s.maxObservedTimestamp.Load() - s.maxOutOfOrderness
	if wm < 0 {
		return 0
	}
	return wm
}

func (s *BoundedOutOfOrdernessStrategy) ObserveEventTime(eventTime int64) {
	for {
		cur := s.maxObservedTimestamp.Load()
		if eventTime <= cur {
			return
		}
		if s.maxObservedTimestamp.CompareAndSwap(cur, eventTime) {
			return
		}
	}
}

// NewMonotonicTimestampsStrategy creates a strategy equivalent to
// BoundedOutOfOrderness with maxOOO=0 (events assumed in order).
func NewMonotonicTimestampsStrategy() *BoundedOutOfOrdernessStrategy {
	return &BoundedOutOfOrdernessStrategy{maxOutOfOrderness: 0}
}

// IngestionTimeStrategy uses the current wall clock as the watermark.
// ObserveEventTime is a no-op — event timestamps are NOT overwritten.
type IngestionTimeStrategy struct {
	clock func() int64 // returns millis; injectable for testing
}

// NewIngestionTimeStrategy creates an ingestion-time strategy using the system clock.
func NewIngestionTimeStrategy() *IngestionTimeStrategy {
	return &IngestionTimeStrategy{
		clock: func() int64 { return time.Now().UnixMilli() },
	}
}

func (s *IngestionTimeStrategy) GenerateWatermark() int64 {
	return s.clock()
}

func (s *IngestionTimeStrategy) ObserveEventTime(_ int64) {
	// No-op: ingestion time does not depend on event timestamps.
}

// legacySourceStrategy wraps a SourceOperator.GenerateWatermark() for backward
// compatibility when no explicit strategy is configured.
type legacySourceStrategy struct {
	source SourceOperator
}

func newLegacySourceStrategy(source SourceOperator) *legacySourceStrategy {
	return &legacySourceStrategy{source: source}
}

func (s *legacySourceStrategy) GenerateWatermark() int64 {
	return s.source.GenerateWatermark()
}

func (s *legacySourceStrategy) ObserveEventTime(_ int64) {
	// No-op: legacy sources manage their own watermark state.
}
