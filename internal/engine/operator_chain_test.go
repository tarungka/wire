package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// -- Mock operators for testing --

type noopMap struct{}

func (n *noopMap) Open(ctx context.Context) error                  { return nil }
func (n *noopMap) Close() error                                    { return nil }
func (n *noopMap) Checkpoint(id uint64) ([]byte, error)            { return nil, nil }
func (n *noopMap) Map(ctx context.Context, e Event) (Event, error) { return e, nil }

type filterMap struct {
	// Pass only events whose Value is non-nil and non-empty.
}

func (f *filterMap) Open(ctx context.Context) error       { return nil }
func (f *filterMap) Close() error                         { return nil }
func (f *filterMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (f *filterMap) Map(ctx context.Context, e Event) (Event, error) {
	if len(e.Value) == 0 {
		return Event{}, nil // Filtered out (zero Event).
	}
	return e, nil
}

type panicMap struct {
	count int
	limit int
}

func (p *panicMap) Open(ctx context.Context) error       { return nil }
func (p *panicMap) Close() error                         { return nil }
func (p *panicMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (p *panicMap) Map(ctx context.Context, e Event) (Event, error) {
	p.count++
	if p.count >= p.limit {
		panic("intentional panic for testing")
	}
	return e, nil
}

type doublerFlatMap struct{}

func (d *doublerFlatMap) Open(ctx context.Context) error       { return nil }
func (d *doublerFlatMap) Close() error                         { return nil }
func (d *doublerFlatMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (d *doublerFlatMap) FlatMap(ctx context.Context, e Event, emit func(Event)) error {
	emit(e)
	emit(Event{Key: e.Key, Value: e.Value, EventTime: e.EventTime + 1})
	return nil
}

type countingSink struct {
	mu    sync.Mutex
	count int
}

func (c *countingSink) Open(ctx context.Context) error       { return nil }
func (c *countingSink) Close() error                         { return nil }
func (c *countingSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (c *countingSink) Write(ctx context.Context, e Event) error {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	return nil
}
func (c *countingSink) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// -- Tests --

func TestOperatorChain_MapPassthrough(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Send 3 events then close.
	for i := 0; i < 3; i++ {
		inputCh <- Event{Value: []byte{byte(i)}}
	}
	close(inputCh)

	ops := []Operator{&noopMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	// Verify 3 output events.
	close(outputCh)
	var got int
	for msg := range outputCh {
		if msg.Type != OutputData {
			t.Errorf("expected OutputData, got %v", msg.Type)
		}
		got++
	}
	if got != 3 {
		t.Fatalf("got %d events, want 3", got)
	}
}

func TestOperatorChain_Filter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Send 3 events: 2 with value, 1 empty.
	inputCh <- Event{Value: []byte("keep")}
	inputCh <- Event{Value: nil}
	inputCh <- Event{Value: []byte("also-keep")}
	close(inputCh)

	ops := []Operator{&filterMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	close(outputCh)
	var got int
	for range outputCh {
		got++
	}
	if got != 2 {
		t.Fatalf("got %d events, want 2 (filtered)", got)
	}
}

func TestOperatorChain_FlatMapOneToMany(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	inputCh <- Event{Value: []byte("x"), EventTime: 100}
	close(inputCh)

	ops := []Operator{&doublerFlatMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	close(outputCh)
	var got []OutputMsg
	for msg := range outputCh {
		got = append(got, msg)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Event.EventTime != 100 {
		t.Errorf("first event time: got %d, want 100", got[0].Event.EventTime)
	}
	if got[1].Event.EventTime != 101 {
		t.Errorf("second event time: got %d, want 101", got[1].Event.EventTime)
	}
}

func TestOperatorChain_Sink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	for i := 0; i < 5; i++ {
		inputCh <- Event{Value: []byte{byte(i)}}
	}
	close(inputCh)

	sink := &countingSink{}
	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	// Sink is terminal — no output messages.
	close(outputCh)
	for msg := range outputCh {
		t.Errorf("unexpected output: %v", msg)
	}

	if sink.Count() != 5 {
		t.Fatalf("sink count: got %d, want 5", sink.Count())
	}
}

func TestOperatorChain_PanicRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	inputCh <- Event{Value: []byte("1")}
	inputCh <- Event{Value: []byte("2")} // This will cause panic.
	close(inputCh)

	ops := []Operator{&panicMap{limit: 2}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())

	if !errors.Is(err, ErrOperatorPanic) {
		t.Fatalf("expected ErrOperatorPanic, got: %v", err)
	}
}

func TestOperatorChain_ControlPriority(t *testing.T) {
	// Verify that control messages are checked with priority in the two-phase
	// select. We test this by sending a shutdown control, verifying the chain
	// terminates promptly (within 1s) even though events are available.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 100)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 100)
	aligner := NewBarrierAligner(1, 100)

	ops := []Operator{&noopMap{}}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	}()

	// Let the chain start and process a few events.
	for i := 0; i < 10; i++ {
		inputCh <- Event{Value: []byte{byte(i)}}
	}
	time.Sleep(50 * time.Millisecond)

	// Send shutdown — the chain should exit promptly.
	controlCh <- ControlMsg{Type: CtrlShutdown}
	// Close inputCh so the chain doesn't block waiting for events.
	close(inputCh)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOperatorChain: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("operator chain did not shut down within 1s after control message")
	}
}

func TestOperatorChain_AllInputsEoP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(2, 100)

	// Send EoP for both inputs.
	controlCh <- ControlMsg{Type: CtrlEndOfPartition, InputIndex: 0}
	controlCh <- ControlMsg{Type: CtrlEndOfPartition, InputIndex: 1}
	close(inputCh) // Close input to not block the operator chain.

	ops := []Operator{&noopMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 2, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	// Should have forwarded an EndOfPartition.
	close(outputCh)
	var foundEnd bool
	for msg := range outputCh {
		if msg.Type == OutputEnd {
			foundEnd = true
		}
	}
	if !foundEnd {
		t.Fatal("expected OutputEnd message")
	}
}

func TestOperatorChain_CheckpointBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Trigger barrier (single input, so immediately aligned).
	aligner.OnBarrier(0, 42, 7)

	controlCh <- ControlMsg{
		Type:         CtrlBarrierReceived,
		InputIndex:   0,
		CheckpointID: 42,
		EpochID:      7,
	}
	close(inputCh)

	ops := []Operator{&noopMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, testLogger())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	close(outputCh)
	var foundBarrier bool
	for msg := range outputCh {
		if msg.Type == OutputBarrier {
			foundBarrier = true
			if msg.Barrier.CheckpointID != 42 {
				t.Errorf("checkpoint ID: got %d, want 42", msg.Barrier.CheckpointID)
			}
		}
	}
	if !foundBarrier {
		t.Fatal("expected OutputBarrier message")
	}
}
