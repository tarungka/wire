package sdk

import (
	"context"
	"encoding/binary"
	"sync"
	"testing"
	"time"
)

type watermarkWindowSource struct {
	sliceSource
	fired <-chan struct{}
}

func (s *watermarkWindowSource) ReadBatch(ctx context.Context) ([]Event, error) {
	s.mu.Lock()
	read := s.read
	s.mu.Unlock()
	if !read {
		return s.sliceSource.ReadBatch(ctx)
	}
	select {
	case <-s.fired:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type watermarkWindowSink struct {
	collectSink
	fired chan struct{}
	once  sync.Once
}

func (s *watermarkWindowSink) Write(ctx context.Context, event Event) error {
	if err := s.collectSink.Write(ctx, event); err != nil {
		return err
	}
	s.once.Do(func() { close(s.fired) })
	return nil
}

func TestEmbeddedWatermarkClosesWindowAcrossShuffle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sink := &watermarkWindowSink{fired: make(chan struct{})}
	source := &watermarkWindowSource{sliceSource: sliceSource{events: []Event{{EventTime: 8}, {EventTime: 2}, {EventTime: 20}}}, fired: sink.fired}
	env := New()
	env.AddSource(source).SetWatermarkStrategy(MonotonicTimestamps().WithEmitInterval(time.Millisecond)).KeyBy(func(Event) ([]byte, error) { return []byte("key"), nil }).Window(TumblingWindow(10 * time.Millisecond)).Aggregate(CountAggregator{}).AddSink(sink)
	if _, err := env.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 1 || events[0].EventTime != 10 || binary.BigEndian.Uint64(events[0].Value) != 2 {
		t.Fatalf("unexpected window results: %+v", events)
	}
}
