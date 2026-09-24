package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func TestWindowTypedSnapshotBackends(t *testing.T) {
	for _, backendKind := range []StateBackendType{StateBackendHashMap, StateBackendPebble} {
		for _, windowKind := range []string{"tumbling", "sliding", "session"} {
			t.Run(string(backendKind)+"/"+windowKind, func(t *testing.T) {
				create := func() *EventTimeWindowOperator {
					op, err := NewEventTimeWindowOperator(WindowConfig{Kind: windowKind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 30, AggregationID: "count"}, windowCount{}, func(r WindowResult) Event { return Event{Key: r.Key, Value: r.Value} })
					if err != nil {
						t.Fatal(err)
					}
					dir := t.TempDir()
					op.SetStateBackendFactory(func() (StateBackend, func(), error) {
						b, err := NewStateBackend(StateBackendConfig{Type: backendKind, PebbleDataDir: dir})
						return b, nil, err
					})
					if err := op.Open(context.Background()); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := op.Close(); err != nil {
							t.Error(err)
						}
					})
					return op
				}
				original := create()
				if err := original.FlatMap(context.Background(), Event{Key: []byte("key"), EventTime: 1}, func(Event) {}); err != nil {
					t.Fatal(err)
				}
				if _, err := original.OnWatermark(context.Background(), 11); err != nil {
					t.Fatal(err)
				}
				snapshot, err := original.CheckpointState(7)
				if err != nil {
					t.Fatal(err)
				}
				if snapshot.BackendType != backendKind {
					t.Fatal("wrong typed backend")
				}
				restored := create()
				if err := restored.RestoreState(snapshot); err != nil {
					t.Fatal(err)
				}
				repeated, err := restored.OnWatermark(context.Background(), 11)
				if err != nil || len(repeated) != 0 {
					t.Fatalf("restore refired unchanged windows: %v %v", repeated, err)
				}
				var updates []Event
				if err := restored.FlatMap(context.Background(), Event{Key: []byte("key"), EventTime: 1}, func(e Event) { updates = append(updates, e) }); err != nil {
					t.Fatal(err)
				}
				if len(updates) == 0 {
					t.Fatal("restored window did not emit update")
				}
				for _, e := range updates {
					if binary.BigEndian.Uint64(e.Value) != 2 {
						t.Fatalf("lost accumulator: %x", e.Value)
					}
				}
				// A valid backend snapshot with invalid window metadata must not replace
				// live backend state or its cached event-time progress.
				if err := original.backend.Put(windowMetadataKey, []byte("bad metadata")); err != nil {
					t.Fatal(err)
				}
				malformed, err := original.CheckpointState(8)
				if err != nil {
					t.Fatal(err)
				}
				if err := restored.RestoreState(malformed); !errors.Is(err, ErrSnapshotCorrupt) {
					t.Fatalf("restore malformed: %v", err)
				}
				updates = nil
				if err := restored.FlatMap(context.Background(), Event{Key: []byte("key"), EventTime: 1}, func(e Event) { updates = append(updates, e) }); err != nil {
					t.Fatal(err)
				}
				if len(updates) == 0 {
					t.Fatal("failed restore damaged live windows")
				}
				for _, e := range updates {
					if binary.BigEndian.Uint64(e.Value) != 3 {
						t.Fatalf("failed restore changed accumulator: %x", e.Value)
					}
				}
			})
		}
	}
}
