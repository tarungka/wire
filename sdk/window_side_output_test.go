package sdk

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type lateSequenceSource struct {
	stage                          int
	initial, updated, purged, late <-chan struct{}
}

func (*lateSequenceSource) Open(context.Context) error { return nil }
func (*lateSequenceSource) Close() error               { return nil }
func (*lateSequenceSource) GenerateWatermark() int64   { return 0 }
func (s *lateSequenceSource) ReadBatch(ctx context.Context) ([]Event, error) {
	phase := s.stage
	s.stage++
	var wait <-chan struct{}
	switch phase {
	case 0:
		return []Event{{Key: []byte("k"), Value: []byte("a"), EventTime: 1}, {Key: []byte("clock"), Value: []byte("clock"), EventTime: 11}}, nil
	case 1:
		wait = s.initial
	case 2:
		wait = s.updated
	case 3:
		wait = s.purged
	default:
		wait = s.late
	}
	select {
	case <-wait:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	switch phase {
	case 1:
		return []Event{{Key: []byte("k"), Value: []byte("b"), EventTime: 1}}, nil
	case 2:
		return []Event{{Key: []byte("clock"), Value: []byte("clock"), EventTime: 40}}, nil
	case 3:
		return []Event{{Key: []byte("k"), Value: []byte("too-late"), EventTime: 1, Headers: map[string][]byte{"original": []byte("yes")}}}, nil
	default:
		return nil, nil
	}
}

type lateSequenceSink struct {
	collectSink
	initial, updated, purged, late             chan struct{}
	firstOnce, updateOnce, purgeOnce, lateOnce sync.Once
}

func (s *lateSequenceSink) Write(ctx context.Context, event Event) error {
	if err := s.collectSink.Write(ctx, event); err != nil {
		return err
	}
	result, ok, err := DecodeWindowResult(event)
	if err != nil {
		return err
	}
	if ok {
		if string(result.Key) == "k" {
			if result.IsUpdate {
				s.updateOnce.Do(func() { close(s.updated) })
			} else {
				s.firstOnce.Do(func() { close(s.initial) })
			}
		}
		if string(result.Key) == "clock" && result.WindowEnd >= 20 {
			s.purgeOnce.Do(func() { close(s.purged) })
		}
	} else if string(event.Value) == "too-late" {
		s.lateOnce.Do(func() { close(s.late) })
	}
	return nil
}
func TestMiniClusterLateOutputAndUpdatesAllWindows(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		for _, mode := range []string{"aggregate", "reduce", "apply"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				signals := &lateSequenceSink{initial: make(chan struct{}), updated: make(chan struct{}), purged: make(chan struct{}), late: make(chan struct{})}
				lateSink := &lateSequenceSink{late: signals.late}
				source := &lateSequenceSource{initial: signals.initial, updated: signals.updated, purged: signals.purged, late: signals.late}
				mc := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
				defer mc.Shutdown()
				env := mc.GetExecutionEnvironment()
				env.SetStateBackend(NewPebbleStateBackend(t.TempDir()))
				var assigner WindowAssigner
				switch kind {
				case "tumbling":
					assigner = TumblingWindow(10 * time.Millisecond)
				case "sliding":
					assigner = SlidingWindow(10*time.Millisecond, 5*time.Millisecond)
				case "session":
					assigner = SessionWindow(10 * time.Millisecond)
				}
				tag := NewOutputTag("late-events")
				window := env.AddSource(source).SetWatermarkStrategy(MonotonicTimestamps().WithEmitInterval(time.Millisecond)).KeyBy(func(e Event) ([]byte, error) { return e.Key, nil }).Window(assigner).AllowedLateness(5 * time.Millisecond).SetLateOutputTag(tag)
				var main *DataStream
				switch mode {
				case "aggregate":
					main = window.Aggregate(CountAggregator{})
				case "reduce":
					main = window.Reduce(func(a, b Event) (Event, error) { return Event{Value: append(a.Value, b.Value...)}, nil })
				case "apply":
					main = window.Apply(func(_ WindowInfo, events []Event) ([]Event, error) {
						var b []byte
						for _, e := range events {
							b = append(b, e.Value...)
						}
						return []Event{{Value: b}}, nil
					})
				}
				main.AddSink(signals)
				main.GetSideOutput(tag).Map(func(e Event) (Event, error) {
					if _, ok, err := DecodeWindowResult(e); err != nil || ok {
						return Event{}, errors.New("late record acquired result metadata")
					}
					return e, nil
				}).AddSink(lateSink)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := env.Execute(ctx); err != nil {
					t.Fatal(err)
				}
				late := lateSink.Events()
				if len(late) != 1 || string(late[0].Value) != "too-late" || string(late[0].Headers["original"]) != "yes" {
					t.Fatalf("late records=%+v", late)
				}
				updated := 0
				for _, event := range signals.Events() {
					if string(event.Value) == "too-late" {
						t.Fatal("late record reached main sink")
					}
					result, ok, err := DecodeWindowResult(event)
					if err != nil || !ok {
						t.Fatal("missing result identity")
					}
					if result.IsUpdate {
						updated++
					}
				}
				if updated != 1 {
					t.Fatalf("updates=%d", updated)
				}
			})
		}
	}
}

func TestYAMLWindowLateOutputExecution(t *testing.T) {
	signals := &lateSequenceSink{initial: make(chan struct{}), updated: make(chan struct{}), purged: make(chan struct{}), late: make(chan struct{})}
	late := &lateSequenceSink{late: signals.late}
	source := &lateSequenceSource{initial: signals.initial, updated: signals.updated, purged: signals.purged, late: signals.late}
	data := `apiVersion: wire/v1
kind: Pipeline
metadata:
  name: lateness
spec:
  sources:
    - name: input
      type: sequence
      watermark:
        strategy: monotonic
        emit_interval: 1ms
  transforms:
    - name: window
      type: tumbling-window
      input: input
      config:
        size: 10ms
        aggregation: count
        allowed_lateness: 5ms
        late_output: expired
  sinks:
    - name: results
      type: main
      input: window
    - name: late
      type: late
      input: expired
`
	pipeline, err := ParsePipelineYAML([]byte(data), PipelineConnectors{Sources: map[string]func(map[string]any) (Source, error){"sequence": func(map[string]any) (Source, error) { return source, nil }}, Sinks: map[string]func(map[string]any) (Sink, error){"main": func(map[string]any) (Sink, error) { return signals, nil }, "late": func(map[string]any) (Sink, error) { return late, nil }}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = pipeline.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if len(late.Events()) != 1 || string(late.Events()[0].Value) != "too-late" {
		t.Fatalf("late=%v", late.Events())
	}
}
