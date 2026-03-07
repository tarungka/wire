package sdk

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
)

// collectSink collects events in a thread-safe slice.
type collectSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *collectSink) Open(_ context.Context) error { return nil }
func (s *collectSink) Write(_ context.Context, event Event) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}
func (s *collectSink) Close() error { return nil }

func (s *collectSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Event, len(s.events))
	copy(result, s.events)
	return result
}

// sliceSource yields events from a slice, one batch. Thread-safe.
type sliceSource struct {
	mu     sync.Mutex
	events []Event
	read   bool
}

func (s *sliceSource) Open(_ context.Context) error { return nil }
func (s *sliceSource) ReadBatch(_ context.Context) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.read {
		return nil, nil
	}
	s.read = true
	return s.events, nil
}
func (s *sliceSource) Close() error             { return nil }
func (s *sliceSource) GenerateWatermark() int64 { return 0 }

func TestEmbeddedSourceMapSink(t *testing.T) {
	env := New()
	sink := &collectSink{}

	env.AddSource(&sliceSource{
		events: []Event{
			{Value: []byte("hello")},
			{Value: []byte("world")},
		},
	}).
		Map(func(e Event) (Event, error) {
			e.Value = append(e.Value, '!')
			return e, nil
		}).
		AddSink(sink)

	result, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("JobResult.Err: %v", result.Err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Check that map was applied.
	for _, e := range events {
		if !bytes.HasSuffix(e.Value, []byte("!")) {
			t.Errorf("expected event value to end with '!', got %q", string(e.Value))
		}
	}
}

func TestEmbeddedSourceFilterSink(t *testing.T) {
	env := New()
	sink := &collectSink{}

	env.AddSource(&sliceSource{
		events: []Event{
			{Value: []byte("keep")},
			{Value: []byte("drop")},
			{Value: []byte("keep")},
		},
	}).
		Filter(func(e Event) (bool, error) {
			return string(e.Value) == "keep", nil
		}).
		AddSink(sink)

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events after filter, got %d", len(events))
	}
}

func TestEmbeddedSourceFlatMapSink(t *testing.T) {
	env := New()
	sink := &collectSink{}

	env.AddSource(&sliceSource{
		events: []Event{
			{Value: []byte("ab")},
		},
	}).
		FlatMap(func(e Event) ([]Event, error) {
			var out []Event
			for _, b := range e.Value {
				out = append(out, Event{Value: []byte{b}})
			}
			return out, nil
		}).
		AddSink(sink)

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events from flatmap, got %d", len(events))
	}
}

func TestEmbeddedMultiOperatorChain(t *testing.T) {
	env := New()
	sink := &collectSink{}

	env.AddSource(&sliceSource{
		events: []Event{
			{Value: []byte("a")},
			{Value: []byte("bb")},
			{Value: []byte("c")},
		},
	}).
		Filter(func(e Event) (bool, error) {
			return len(e.Value) == 1, nil // Keep single-char values.
		}).
		Map(func(e Event) (Event, error) {
			e.Value = bytes.ToUpper(e.Value)
			return e, nil
		}).
		AddSink(sink)

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for _, e := range events {
		if !bytes.Equal(bytes.ToUpper(e.Value), e.Value) {
			t.Errorf("expected uppercase, got %q", string(e.Value))
		}
	}
}

func TestEmbeddedContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Source that blocks until context is cancelled.
	src := &blockingSource{ctx: ctx}

	env := New()
	env.AddSource(src).AddSink(&collectSink{})

	done := make(chan struct{})
	var execErr error
	go func() {
		_, execErr = env.Execute(ctx)
		close(done)
	}()

	cancel()
	<-done

	// Should complete without hanging; error may be nil or context.Canceled.
	_ = execErr
}

// blockingSource blocks on ReadBatch until context is cancelled.
type blockingSource struct {
	ctx context.Context
}

func (s *blockingSource) Open(_ context.Context) error { return nil }
func (s *blockingSource) ReadBatch(ctx context.Context) ([]Event, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (s *blockingSource) Close() error             { return nil }
func (s *blockingSource) GenerateWatermark() int64 { return 0 }

func TestEmbeddedErrorPropagation(t *testing.T) {
	env := New()

	env.AddSource(&sliceSource{
		events: []Event{{Value: []byte("x")}},
	}).
		Map(func(e Event) (Event, error) {
			return Event{}, fmt.Errorf("intentional error")
		}).
		AddSink(&collectSink{})

	_, err := env.Execute(context.Background())
	if err == nil {
		t.Fatal("expected error from map operator")
	}
}

func TestEmbeddedParallelism(t *testing.T) {
	// Create multiple sources with parallelism > 1.
	// Each parallel instance gets its own source instance, so with p=1
	// and multiple events we verify basic flow.
	env := New()
	env.SetParallelism(1)
	sink := &collectSink{}

	events := make([]Event, 100)
	for i := range events {
		events[i] = Event{Value: []byte(fmt.Sprintf("e%d", i))}
	}

	env.AddSource(&sliceSource{events: events}).
		Map(func(e Event) (Event, error) { return e, nil }).
		AddSink(sink)

	_, err := env.Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	got := sink.Events()
	if len(got) != 100 {
		t.Errorf("expected 100 events, got %d", len(got))
	}
}
