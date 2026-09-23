package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
)

func TestWindowPebblePurgeReopenAndPortableRestore(t *testing.T) {
	for _, kind := range []string{"tumbling", "sliding", "session"} {
		t.Run(kind, func(t *testing.T) {
			config := WindowConfig{Kind: kind, Size: 10, Slide: 5, Gap: 10, AllowedLateness: 5}
			processor := newTestWindow(t, config)
			dir := t.TempDir()
			open := func() StateBackend {
				b, err := NewStateBackend(StateBackendConfig{Type: StateBackendPebble, PebbleDataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			backend := open()
			if err := processor.BindBackend(backend); err != nil {
				t.Fatal(err)
			}
			addWindowEvent(t, processor, 1)
			processor.AdvanceWatermark(11)
			snapshot, err := processor.Checkpoint(7)
			if err != nil {
				t.Fatal(err)
			}
			if err = backend.Close(); err != nil {
				t.Fatal(err)
			}
			backend = open()
			defer backend.Close()
			recovered := newTestWindow(t, config)
			if err = recovered.BindBackend(backend); err != nil {
				t.Fatal(err)
			}
			if got := recovered.AdvanceWatermark(11); len(got) != 0 {
				t.Fatal("reopened state refired")
			}
			events, late := addWindowEvent(t, recovered, 1)
			if late || len(events) != 1 || !events[0].IsUpdate || binary.BigEndian.Uint64(events[0].Value) != 2 {
				t.Fatalf("reopened state lost update: %+v late=%t", events, late)
			}
			recovered.AdvanceWatermark(30)
			it := backend.NewIterator(windowRecordsPrefix)
			if it.Next() {
				t.Fatal("purged windows remain in Pebble")
			}
			it.Close()
			if recovered.Stats().RetentionBytes != 0 || recovered.Stats().StateBytes != 0 {
				t.Fatal("purge retained payload")
			}
			// Restore an earlier portable checkpoint, rewinding progress and removing
			// any post-checkpoint state before accepting replayed records.
			if err = recovered.Restore(snapshot); err != nil {
				t.Fatal(err)
			}
			events, late = addWindowEvent(t, recovered, 1)
			if late || len(events) != 1 || !events[0].IsUpdate || binary.BigEndian.Uint64(events[0].Value) != 2 {
				t.Fatal("portable restore lost firing identity")
			}
		})
	}
}

type failingWindowBatch struct {
	*HashMapStateBackend
	fail bool
}

func (b *failingWindowBatch) ApplyBatch(changes []StateMutation) error {
	if b.fail {
		return errors.New("disk unavailable")
	}
	return b.HashMapStateBackend.ApplyBatch(changes)
}
func TestWindowBackendFailureDoesNotAdvanceState(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10, AllowedLateness: 5})
	store := &failingWindowBatch{HashMapStateBackend: NewHashMapStateBackend(0)}
	if err := p.BindBackend(store); err != nil {
		t.Fatal(err)
	}
	addWindowEvent(t, p, 1)
	store.fail = true
	if _, _, err := p.Process(Event{Key: []byte("k"), EventTime: 2}); err == nil {
		t.Fatal("accepted failed state write")
	}
	if _, err := p.AdvanceWatermarkChecked(10); err == nil {
		t.Fatal("ignored failed watermark persistence")
	}
	store.fail = false
	results, err := p.AdvanceWatermarkChecked(10)
	if err != nil || len(results) != 1 || binary.BigEndian.Uint64(results[0].Value) != 1 || results[0].IsUpdate {
		t.Fatalf("failure mutated state: %+v %v", results, err)
	}
}
func TestWindowPayloadLimitRejectsAtomicGrowth(t *testing.T) {
	p := newTestWindow(t, WindowConfig{Kind: "tumbling", Size: 10, MaxStateBytes: 9})
	addWindowEvent(t, p, 1)
	if _, _, err := p.Process(Event{Key: []byte("another"), EventTime: 1}); !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatal(err)
	}
	if p.Stats().StateBytes != 9 || p.Stats().RetainedWindows != 1 {
		t.Fatal("limit changed retained state")
	}
}
func TestWindowOperatorOpensConfiguredBackend(t *testing.T) {
	store := NewHashMapStateBackend(0)
	op, err := NewEventTimeWindowOperator(WindowConfig{Kind: "tumbling", Size: 10, AggregationID: "count"}, windowCount{}, func(r WindowResult) Event { return Event{Value: r.Value} })
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	op.StateBackendFactory = func() (StateBackend, func(), error) { return store, func() { cleaned = true }, nil }
	if err = op.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = op.FlatMap(context.Background(), Event{EventTime: 1}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	iterator := store.NewIterator(windowRecordsPrefix)
	if !iterator.Next() {
		t.Fatal("runtime failed to persist state")
	}
	iterator.Close()
	if err = op.Close(); err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("backend cleanup not called")
	}
	if _, err = store.Get(windowMetadataKey); !errors.Is(err, ErrBackendClosed) {
		t.Fatal(err)
	}
}
