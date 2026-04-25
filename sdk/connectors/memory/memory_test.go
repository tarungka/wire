package memory_test

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/worker"
	"github.com/tarungka/wire/sdk/connectors/memory"
)

func mustEncode(t *testing.T, v any) []byte {
	t.Helper()
	b, err := protocol.EncodeMsgPack(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestMemorySource_EmitsEventsThenEOF(t *testing.T) {
	cfg := mustEncode(t, memory.SourceConfig{Events: [][]byte{
		[]byte("a"), []byte("b"), []byte("c"),
	}})

	op, err := memory.SourceFactory()(context.Background(), cfg, worker.TaskContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if err := op.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer op.Close()

	batch1, err := op.ReadBatch(context.Background())
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if len(batch1) != 3 {
		t.Fatalf("first batch len: got %d want 3", len(batch1))
	}
	for i, ev := range batch1 {
		if string(ev.Value) != string([]byte{byte('a' + i)}) {
			t.Errorf("batch[%d]: got %q want %q", i, ev.Value, []byte{byte('a' + i)})
		}
	}

	// Subsequent reads should return nil to signal EOF.
	batch2, err := op.ReadBatch(context.Background())
	if err != nil {
		t.Fatalf("ReadBatch (2): %v", err)
	}
	if batch2 != nil {
		t.Fatalf("expected nil batch on EOF, got %v", batch2)
	}
}

func TestMemorySource_EmptyConfig(t *testing.T) {
	op, err := memory.SourceFactory()(context.Background(), nil, worker.TaskContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	batch, err := op.ReadBatch(context.Background())
	if err != nil {
		t.Fatalf("ReadBatch: %v", err)
	}
	if len(batch) != 0 {
		t.Fatalf("empty source should produce empty batch, got %d events", len(batch))
	}
}

func TestMemorySource_BadConfig(t *testing.T) {
	_, err := memory.SourceFactory()(context.Background(), []byte{0xff, 0xfe, 0xfd}, worker.TaskContext{})
	if err == nil {
		t.Fatal("expected decode error on garbage config")
	}
}

func TestMemorySink_CapturesAndResets(t *testing.T) {
	id := "test-sink-capture"
	memory.Reset(id)
	t.Cleanup(func() { memory.Reset(id) })

	cfg := mustEncode(t, memory.SinkConfig{SinkID: id})
	op, err := memory.SinkFactory()(context.Background(), cfg, worker.TaskContext{})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	defer op.Close()

	want := [][]byte{[]byte("x"), []byte("y"), []byte("z")}
	for _, v := range want {
		if err := op.Write(context.Background(), engine.Event{Value: v}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got := memory.Collected(id)
	if len(got) != len(want) {
		t.Fatalf("collected len: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i].Value) != string(want[i]) {
			t.Errorf("collected[%d]: got %q want %q", i, got[i].Value, want[i])
		}
	}

	// Reset the specific id and verify the slice is now empty.
	memory.Reset(id)
	if got := memory.Collected(id); len(got) != 0 {
		t.Fatalf("after Reset, collected should be empty; got %d", len(got))
	}
}

func TestMemorySink_CollectedReturnsCopy(t *testing.T) {
	id := "test-sink-copy"
	memory.Reset(id)
	t.Cleanup(func() { memory.Reset(id) })

	cfg := mustEncode(t, memory.SinkConfig{SinkID: id})
	op, _ := memory.SinkFactory()(context.Background(), cfg, worker.TaskContext{})
	_ = op.Write(context.Background(), engine.Event{Value: []byte("v")})

	first := memory.Collected(id)
	if len(first) != 1 {
		t.Fatalf("expected 1, got %d", len(first))
	}
	first[0].Value = []byte("MUTATED")

	second := memory.Collected(id)
	if string(second[0].Value) != "v" {
		t.Fatalf("Collected should return a copy; got mutation %q", second[0].Value)
	}
}

func TestMemorySink_IsolationByID(t *testing.T) {
	idA := "isolated-A"
	idB := "isolated-B"
	memory.Reset(idA)
	memory.Reset(idB)
	t.Cleanup(func() { memory.Reset(idA); memory.Reset(idB) })

	mkSink := func(id string) engine.SinkOperator {
		cfg := mustEncode(t, memory.SinkConfig{SinkID: id})
		op, err := memory.SinkFactory()(context.Background(), cfg, worker.TaskContext{})
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		return op
	}

	a := mkSink(idA)
	b := mkSink(idB)
	_ = a.Write(context.Background(), engine.Event{Value: []byte("a1")})
	_ = b.Write(context.Background(), engine.Event{Value: []byte("b1")})
	_ = a.Write(context.Background(), engine.Event{Value: []byte("a2")})

	gotA := memory.Collected(idA)
	gotB := memory.Collected(idB)
	if len(gotA) != 2 || string(gotA[0].Value) != "a1" || string(gotA[1].Value) != "a2" {
		t.Fatalf("sink A mismatch: %v", valuesOf(gotA))
	}
	if len(gotB) != 1 || string(gotB[0].Value) != "b1" {
		t.Fatalf("sink B mismatch: %v", valuesOf(gotB))
	}
}

func valuesOf(events []engine.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Value)
	}
	return out
}
