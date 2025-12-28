package runtime

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/tarungka/wire/internal/analytics"
)

// BoundedStream implements analytics.Stream with water-level based backpressure.
type BoundedStream struct {
	buffer chan *analytics.Record
	closed atomic.Bool

	// Backpressure settings
	highWatermark int
	lowWatermark  int

	// Current state
	count atomic.Int64

	// Cond for backpressure
	mu      sync.Mutex
	paused  bool
	canEmit *sync.Cond
}

// NewBoundedStream creates a new BoundedStream with the given capacity and watermarks.
func NewBoundedStream(capacity, highWatermark, lowWatermark int) *BoundedStream {
	s := &BoundedStream{
		buffer:        make(chan *analytics.Record, capacity),
		highWatermark: highWatermark,
		lowWatermark:  lowWatermark,
	}
	s.canEmit = sync.NewCond(&s.mu)
	return s
}

// Emit sends a record to the stream. Blocks if paused due to high watermark.
func (s *BoundedStream) Emit(record *analytics.Record) error {
	if s.closed.Load() {
		return net.ErrClosed // Or a custom error
	}

	s.mu.Lock()
	for s.paused {
		s.canEmit.Wait()
	}
	s.mu.Unlock()

	s.buffer <- record
	newCount := s.count.Add(1)

	if int(newCount) >= s.highWatermark {
		s.mu.Lock()
		s.paused = true
		s.mu.Unlock()
	}

	return nil
}

// Consume returns the channel for reading records.
func (s *BoundedStream) Consume() <-chan *analytics.Record {
	return s.buffer
}

// Acknowledge is called by the consumer when a record is processed.
// It decrements the counter and potentially signals the producer to resume.
func (s *BoundedStream) Acknowledge() {
	newCount := s.count.Add(-1)

	if int(newCount) <= s.lowWatermark {
		s.mu.Lock()
		if s.paused {
			s.paused = false
			s.canEmit.Broadcast()
		}
		s.mu.Unlock()
	}
}

// Close closes the stream.
func (s *BoundedStream) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		close(s.buffer)
	}
	return nil
}
