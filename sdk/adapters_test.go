package sdk

import (
	"context"
	"testing"
)

func TestMapAdapter(t *testing.T) {
	adapter := &mapAdapter{fn: func(e Event) (Event, error) {
		e.Value = append(e.Value, '!')
		return e, nil
	}}

	if err := adapter.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	out, err := adapter.Map(context.Background(), Event{Value: []byte("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if string(out.Value) != "hello!" {
		t.Errorf("expected 'hello!', got %q", string(out.Value))
	}

	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFlatMapAdapter(t *testing.T) {
	adapter := &flatMapAdapter{fn: func(e Event) ([]Event, error) {
		return []Event{
			{Value: []byte("a")},
			{Value: []byte("b")},
			{Value: []byte("c")},
		}, nil
	}}

	var emitted []Event
	err := adapter.FlatMap(context.Background(), Event{}, func(e Event) {
		emitted = append(emitted, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 3 {
		t.Fatalf("expected 3 emitted events, got %d", len(emitted))
	}
	if string(emitted[0].Value) != "a" || string(emitted[1].Value) != "b" || string(emitted[2].Value) != "c" {
		t.Errorf("unexpected emitted values")
	}
}

func TestFilterAdapterKeep(t *testing.T) {
	adapter := &filterAdapter{fn: func(e Event) (bool, error) {
		return true, nil
	}}

	out, err := adapter.Map(context.Background(), Event{Value: []byte("keep")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Value == nil {
		t.Error("expected event to be kept, got zero event")
	}
}

func TestFilterAdapterDrop(t *testing.T) {
	adapter := &filterAdapter{fn: func(e Event) (bool, error) {
		return false, nil
	}}

	out, err := adapter.Map(context.Background(), Event{Value: []byte("drop")})
	if err != nil {
		t.Fatal(err)
	}
	// Zero event = filtered.
	if out.Key != nil || out.Value != nil || out.EventTime != 0 || out.Headers != nil {
		t.Error("expected zero event for dropped event")
	}
}

func TestSourceAdapter(t *testing.T) {
	src := &memorySource{
		events: []Event{{Value: []byte("e1")}, {Value: []byte("e2")}},
	}
	adapter := &sourceAdapter{source: src}

	if err := adapter.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	batch, err := adapter.ReadBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("expected 2 events, got %d", len(batch))
	}

	// Second call should return nil (end of input).
	batch, err = adapter.ReadBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Error("expected nil batch at end of input")
	}

	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSinkAdapter(t *testing.T) {
	sink := &memorySink{}
	adapter := &sinkAdapter{sink: sink}

	if err := adapter.Open(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := adapter.Write(context.Background(), Event{Value: []byte("out")}); err != nil {
		t.Fatal(err)
	}

	if len(sink.events) != 1 || string(sink.events[0].Value) != "out" {
		t.Error("expected event to be written to sink")
	}

	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
}

// memorySource is a test Source that yields events from a slice.
type memorySource struct {
	events []Event
	read   bool
}

func (s *memorySource) Open(_ context.Context) error { return nil }
func (s *memorySource) ReadBatch(_ context.Context) ([]Event, error) {
	if s.read {
		return nil, nil
	}
	s.read = true
	return s.events, nil
}
func (s *memorySource) Close() error             { return nil }
func (s *memorySource) GenerateWatermark() int64 { return 0 }

// memorySink is a test Sink that collects events in a slice.
type memorySink struct {
	events []Event
}

func (s *memorySink) Open(_ context.Context) error { return nil }
func (s *memorySink) Write(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}
func (s *memorySink) Close() error { return nil }
