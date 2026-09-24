package engine

import (
	"math"
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
// a configurable bound. The watermark is maxObserved - maxOOO, saturating at MinInt64.
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
	s := &BoundedOutOfOrdernessStrategy{maxOutOfOrderness: maxOOO.Milliseconds()}
	s.maxObservedTimestamp.Store(math.MinInt64)
	return s
}

func (s *BoundedOutOfOrdernessStrategy) GenerateWatermark() int64 {
	observed := s.maxObservedTimestamp.Load()
	if observed < math.MinInt64+s.maxOutOfOrderness {
		return math.MinInt64
	}
	return observed - s.maxOutOfOrderness
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
	s := &BoundedOutOfOrdernessStrategy{}
	s.maxObservedTimestamp.Store(math.MinInt64)
	return s
}

// IngestionTimeStrategy uses the current wall clock as the watermark.
// The source reader assigns timestamps from this clock at ingestion.
// ObserveEventTime is a no-op because producer timestamps are not used.
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
