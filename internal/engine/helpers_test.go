package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// testLogger returns a no-op zerolog logger for tests.
func testLogger() zerolog.Logger {
	return zerolog.Nop()
}

// -- Mock operators used across test files --

// mockSource produces predefined batches and tracks watermark generation.
type mockSource struct {
	mu        sync.Mutex
	batches   [][]Event
	batchIdx  int
	watermark int64
}

func newMockSource(batches [][]Event) *mockSource {
	return &mockSource{batches: batches}
}

func (m *mockSource) Open(ctx context.Context) error       { return nil }
func (m *mockSource) Close() error                         { return nil }
func (m *mockSource) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (m *mockSource) ReadBatch(ctx context.Context) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.batchIdx >= len(m.batches) {
		return nil, nil // End of input.
	}
	batch := m.batches[m.batchIdx]
	m.batchIdx++
	return batch, nil
}

func (m *mockSource) GenerateWatermark() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watermark
}

func (m *mockSource) SetWatermark(ts int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.watermark = ts
}

// slowSink sleeps on each Write call for backpressure testing.
type slowSink struct {
	mu    sync.Mutex
	count int
	delay time.Duration
}

func newSlowSink(delay time.Duration) *slowSink {
	return &slowSink{delay: delay}
}

func (s *slowSink) Open(ctx context.Context) error       { return nil }
func (s *slowSink) Close() error                         { return nil }
func (s *slowSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }

func (s *slowSink) Write(ctx context.Context, e Event) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	return nil
}

func (s *slowSink) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// atomicCountingSink is a thread-safe counting sink.
type atomicCountingSink struct {
	count atomic.Int64
}

func (a *atomicCountingSink) Open(ctx context.Context) error       { return nil }
func (a *atomicCountingSink) Close() error                         { return nil }
func (a *atomicCountingSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (a *atomicCountingSink) Write(ctx context.Context, e Event) error {
	a.count.Add(1)
	return nil
}
