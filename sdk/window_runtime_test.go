package sdk

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

func TestWindowRuntimeUpdates(t *testing.T) {
	w, err := newWindowRuntime(&StreamNode{Type: NodeWindow, Window: TumblingWindow(10 * time.Millisecond), Aggregator: CountAggregator{}, AllowedLateness: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.process(Event{Key: []byte("k"), EventTime: 1}); err != nil {
		t.Fatal(err)
	}
	out, err := w.watermark(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || binary.BigEndian.Uint64(out[0].Value) != 1 || string(out[0].Headers["wire.window.update"]) != "false" {
		t.Fatalf("initial: %+v", out)
	}
	out, err = w.process(Event{Key: []byte("k"), EventTime: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || binary.BigEndian.Uint64(out[0].Value) != 2 || string(out[0].Headers["wire.window.update"]) != "true" {
		t.Fatalf("update: %+v", out)
	}
}

func TestEmbeddedWindowPipeline(t *testing.T) {
	env := New()
	sink := &collectSink{}
	env.AddSource(&sliceSource{events: []Event{
		{Value: []byte("a"), EventTime: 1}, {Value: []byte("a"), EventTime: 2}, {Value: []byte("b"), EventTime: 3},
	}}).KeyBy(func(e Event) ([]byte, error) { return e.Value, nil }).Window(TumblingWindow(10 * time.Millisecond)).Aggregate(CountAggregator{}).AddSink(sink)
	result, err := env.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("expected two keyed windows: %+v", events)
	}
	counts := map[string]uint64{}
	for _, e := range events {
		counts[string(e.Key)] = binary.BigEndian.Uint64(e.Value)
		if e.EventTime != 10 {
			t.Fatalf("window end: %d", e.EventTime)
		}
	}
	if counts["a"] != 2 || counts["b"] != 1 {
		t.Fatalf("keyed counts: %v", counts)
	}
}

type lateWindowSource struct {
	sliceSource
	index int
}

func (s *lateWindowSource) ReadBatch(context.Context) ([]Event, error) {
	s.index++
	switch s.index {
	case 1:
		return []Event{{EventTime: 1}}, nil
	case 2:
		return []Event{{EventTime: 2}}, nil
	case 3:
		return []Event{{EventTime: 3}}, nil
	default:
		return nil, nil
	}
}
func (s *lateWindowSource) GenerateWatermark() int64 {
	if s.index == 1 {
		return 10
	}
	return 15
}

func TestEmbeddedWindowApplyLateUpdate(t *testing.T) {
	env := New()
	sink := &collectSink{}
	env.AddSource(&lateWindowSource{}).KeyBy(func(Event) ([]byte, error) { return []byte("k"), nil }).Window(TumblingWindow(10 * time.Millisecond)).AllowedLateness(5).Apply(func(info WindowInfo, events []Event) ([]Event, error) {
		if info.Start != 0 || info.End != 10 {
			t.Errorf("bounds %+v", info)
		}
		return []Event{{Value: []byte{byte(len(events))}}}, nil
	}).AddSink(sink)
	if _, err := env.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 2 || events[0].Value[0] != 1 || events[1].Value[0] != 2 || string(events[1].Headers["wire.window.update"]) != "true" {
		t.Fatalf("late results: %+v", events)
	}
}

func TestEmbeddedSessionReduce(t *testing.T) {
	env := New()
	sink := &collectSink{}
	env.AddSource(&sliceSource{events: []Event{{EventTime: 1, Value: []byte{1}}, {EventTime: 5, Value: []byte{2}}, {EventTime: 30, Value: []byte{4}}}}).KeyBy(func(Event) ([]byte, error) { return []byte("k"), nil }).Window(SessionWindow(10 * time.Millisecond)).Reduce(func(a, b Event) (Event, error) { a.Value = []byte{a.Value[0] + b.Value[0]}; return a, nil }).AddSink(sink)
	if _, err := env.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) != 2 || events[0].Value[0] != 3 || events[0].EventTime != 15 || events[1].Value[0] != 4 || events[1].EventTime != 40 {
		t.Fatalf("session reductions: %+v", events)
	}
}

func TestEmbeddedWindowReduceError(t *testing.T) {
	env := New()
	sink := &collectSink{}
	failure := errors.New("reduce failed")
	env.AddSource(&sliceSource{events: []Event{{EventTime: 1}, {EventTime: 2}}}).KeyBy(func(Event) ([]byte, error) { return nil, nil }).Window(TumblingWindow(time.Second)).Reduce(func(Event, Event) (Event, error) { return Event{}, failure }).AddSink(sink)
	if _, err := env.Execute(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("lost reduce error: %v", err)
	}
	if len(sink.Events()) != 0 {
		t.Fatal("failed reduction emitted output")
	}
}

type windowOpenProbe struct {
	sliceSource
	opened bool
}

func (s *windowOpenProbe) Open(context.Context) error { s.opened = true; return nil }
func TestWindowUnsupportedSettingsBeforeOpen(t *testing.T) {
	for _, configure := range []func(*StreamExecutionEnvironment){
		func(e *StreamExecutionEnvironment) { e.SetParallelism(2) },
		func(e *StreamExecutionEnvironment) { e.SetCheckpointInterval(time.Second) },
		func(e *StreamExecutionEnvironment) { e.SetRestartStrategy(FixedDelay(1, time.Second)) },
	} {
		env := New()
		configure(env)
		source := &windowOpenProbe{}
		env.AddSource(source).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).Window(TumblingWindow(time.Second)).Aggregate(CountAggregator{}).AddSink(&collectSink{})
		if _, err := env.Execute(context.Background()); err == nil {
			t.Fatal("unsupported execution accepted")
		}
		if source.opened {
			t.Fatal("source opened before validation")
		}
	}
}
