package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type concurrentWatermarkSource struct {
	value    atomic.Int64
	reading  atomic.Bool
	overlap  chan struct{}
	once     sync.Once
	finished bool
	closed   atomic.Bool
}

func (*concurrentWatermarkSource) Open(context.Context) error        { return nil }
func (s *concurrentWatermarkSource) Close() error                    { s.closed.Store(true); return nil }
func (*concurrentWatermarkSource) Checkpoint(uint64) ([]byte, error) { return nil, nil }
func (s *concurrentWatermarkSource) ReadBatch(ctx context.Context) ([]Event, error) {
	if s.finished {
		return nil, nil
	}
	s.reading.Store(true)
	defer s.reading.Store(false)
	s.value.Store(1234567890123)
	select {
	case <-s.overlap:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.finished = true
	return []Event{{Value: []byte("record"), EventTime: s.value.Load()}}, nil
}
func (s *concurrentWatermarkSource) GenerateWatermark() int64 {
	value := s.value.Load()
	if s.reading.Load() && value == 1234567890123 {
		s.once.Do(func() { close(s.overlap) })
	}
	return value
}

func TestSourceReadAndWatermarkOverlapInTaskLifecycle(t *testing.T) {
	source := &concurrentWatermarkSource{overlap: make(chan struct{})}
	sink := &atomicCountingSink{}
	config := DefaultTaskSlotConfig()
	config.WatermarkInterval = time.Millisecond
	config.Watermark.EmitInterval = time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	slot := NewTaskSlot(config, nil, nil, []Operator{sink}, source)
	if err := slot.Run(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.overlap:
	default:
		t.Fatal("watermark generation never overlapped source read")
	}
	if sink.count.Load() != 1 || !source.closed.Load() || source.reading.Load() {
		t.Fatal("source lifecycle did not process and join cleanly")
	}
}
