package sdk

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
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
	if len(events) != 2 || events[0].EventTime != 10 || binary.BigEndian.Uint64(events[0].Value) != 2 || events[1].EventTime != 30 || binary.BigEndian.Uint64(events[1].Value) != 1 {
		t.Fatalf("unexpected window results: %+v", events)
	}
}

func TestWindowReduceAndApplyPreserveLateUpdates(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		for _, mode := range []string{"reduce", "apply"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				var assigner WindowAssigner
				switch kind {
				case "tumbling":
					assigner = TumblingWindow(10 * time.Millisecond)
				case "sliding":
					assigner = SlidingWindow(10*time.Millisecond, 5*time.Millisecond)
				case "session":
					assigner = SessionWindow(10 * time.Millisecond)
				}
				node := &StreamNode{ID: 1, Window: assigner, AllowedLateness: 5}
				if mode == "reduce" {
					node.ReduceFn = func(a, b Event) (Event, error) { return Event{Value: append(a.Value, b.Value...)}, nil }
				} else {
					node.WindowFn = func(info WindowInfo, events []Event) ([]Event, error) {
						if info.Start >= info.End {
							t.Fatal("missing window bounds")
						}
						if info.IsUpdate != (len(events) > 1) {
							t.Fatalf("Apply update identity=%t for %d retained records", info.IsUpdate, len(events))
						}
						var data []byte
						for _, event := range events {
							data = append(data, event.Value...)
						}
						return []Event{{Value: data}}, nil
					}
				}
				built, err := embeddedWindow(node)
				if err != nil {
					t.Fatal(err)
				}
				op := built.(*engine.EventTimeWindowOperator)
				if err = op.FlatMap(t.Context(), Event{Key: []byte("k"), Value: []byte("a"), EventTime: 1}, func(Event) { t.Fatal("early result") }); err != nil {
					t.Fatal(err)
				}
				events, err := op.OnWatermark(t.Context(), 11)
				if err != nil || len(events) == 0 {
					t.Fatalf("%v %v", events, err)
				}
				for _, event := range events {
					r, ok, err := DecodeWindowResult(event)
					if err != nil || !ok || r.IsUpdate || string(event.Value) != "a" {
						t.Fatalf("bad initial %+v %v", r, err)
					}
				}
				var updates []Event
				if err = op.FlatMap(t.Context(), Event{Key: []byte("k"), Value: []byte("b"), EventTime: 1}, func(e Event) { updates = append(updates, e) }); err != nil {
					t.Fatal(err)
				}
				if len(updates) != 1 {
					t.Fatalf("updates=%v", updates)
				}
				result, _, err := DecodeWindowResult(updates[0])
				if err != nil || !result.IsUpdate || string(result.Value) != "ab" {
					t.Fatalf("bad update %+v %v", result, err)
				}
				snapshot, err := op.Checkpoint(3)
				if err != nil {
					t.Fatal(err)
				}
				restored, err := embeddedWindow(node)
				if err != nil {
					t.Fatal(err)
				}
				target := restored.(*engine.EventTimeWindowOperator)
				if err = target.RestoreCheckpoint(snapshot); err != nil {
					t.Fatal(err)
				}
				if err = target.FlatMap(t.Context(), Event{Key: []byte("k"), Value: []byte("c"), EventTime: 1}, func(event Event) {
					if string(event.Value) != "abc" {
						t.Fatalf("restored result=%s", event.Value)
					}
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestWindowReduceErrorLeavesAccumulatorIntact(t *testing.T) {
	fail := true
	node := &StreamNode{ID: 2, Window: TumblingWindow(10 * time.Millisecond), ReduceFn: func(a, b Event) (Event, error) {
		if fail {
			return Event{}, errors.New("reduce failed")
		}
		return Event{Value: append(a.Value, b.Value...)}, nil
	}}
	built, err := embeddedWindow(node)
	if err != nil {
		t.Fatal(err)
	}
	op := built.(*engine.EventTimeWindowOperator)
	if err = op.FlatMap(t.Context(), Event{Value: []byte("a"), EventTime: 1}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if err = op.FlatMap(t.Context(), Event{Value: []byte("b"), EventTime: 2}, func(Event) {}); err == nil {
		t.Fatal("reduction failure ignored")
	}
	fail = false
	events, err := op.OnWatermark(t.Context(), 10)
	if err != nil || len(events) != 1 || string(events[0].Value) != "a" {
		t.Fatalf("failed reduction changed state %v %v", events, err)
	}
}
