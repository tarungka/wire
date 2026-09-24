package sdk

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
)

func TestMiniClusterDefaultBackendAndPebbleOverride(t *testing.T) {
	for _, kind := range []engine.StateBackendType{engine.StateBackendHashMap, engine.StateBackendPebble} {
		t.Run(string(kind), func(t *testing.T) {
			cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
			defer cluster.Shutdown()
			env := cluster.GetExecutionEnvironment()
			if env.stateBackend.Type != "hashmap" || env.stateBackend.MaxMemoryMB != 256 {
				t.Fatal("MiniCluster does not use the bounded HashMap default")
			}
			if kind == engine.StateBackendPebble {
				env.SetStateBackend(NewPebbleStateBackend(t.TempDir()))
			}
			sink := &collectSink{}
			env.AddSource(&sliceSource{events: []Event{{Value: []byte("first")}, {Value: []byte("second")}}}).
				KeyBy(func(Event) ([]byte, error) { return []byte("same-key"), nil }).
				Process(func(c ProcessContext, e Event) ([]Event, error) {
					state := c.GetValueState("value")
					previous := state.Get()
					state.Set(e.Value)
					backend := c.(*backendProcessContext).backend
					snapshot, err := backend.Checkpoint(7)
					if err != nil {
						return nil, err
					}
					if snapshot.BackendType != kind {
						return nil, fmt.Errorf("worker opened %s instead of %s", snapshot.BackendType, kind)
					}
					e.Value = append(append([]byte(nil), previous...), e.Value...)
					return []Event{e}, nil
				}).AddSink(sink)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if _, err := env.Execute(ctx); err != nil {
				t.Fatal(err)
			}
			events := sink.Events()
			if len(events) != 2 || string(events[0].Value) != "first" || string(events[1].Value) != "firstsecond" {
				t.Fatalf("backend lost keyed state: %v", events)
			}
		})
	}
}

// Startup measures construction through the first stateful invocation on real
// MiniCluster workers. ns/op also includes job completion and runtime shutdown.
func BenchmarkMiniClusterStateBackendLifecycle(b *testing.B) {
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	b.Cleanup(func() { zerolog.SetGlobalLevel(level) })
	for _, kind := range []string{"hashmap", "pebble"} {
		b.Run(kind, func(b *testing.B) {
			b.ReportAllocs()
			var startup time.Duration
			for i := 0; i < b.N; i++ {
				started := time.Now()
				cluster := NewMiniCluster(MiniClusterConfig{NumTaskSlots: 1})
				env := cluster.GetExecutionEnvironment()
				if kind == "pebble" {
					env.SetStateBackend(NewPebbleStateBackend(""))
				}
				sink := &collectSink{}
				env.AddSource(&sliceSource{events: []Event{{Value: []byte("record")}}}).
					KeyBy(func(Event) ([]byte, error) { return []byte("key"), nil }).
					Process(func(c ProcessContext, e Event) ([]Event, error) {
						c.GetValueState("value").Set(e.Value)
						startup += time.Since(started)
						return []Event{e}, nil
					}).AddSink(sink)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, err := env.Execute(ctx)
				cancel()
				closeErr := cluster.Shutdown()
				if err != nil {
					b.Fatal(err)
				}
				if closeErr != nil {
					b.Fatal(closeErr)
				}
				if len(sink.Events()) != 1 {
					b.Fatal("benchmark did not execute record")
				}
			}
			b.ReportMetric(float64(startup.Nanoseconds())/float64(b.N), "startup-ns/op")
		})
	}
}
