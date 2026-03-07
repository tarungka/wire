package engine

import (
	"context"
	"errors"
	"fmt"
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())

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
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
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

// -- Additional mock operators for lifecycle tests --

// lifecycleOp records Open/Close calls with ordering.
type lifecycleOp struct {
	name     string
	log      *[]string
	openErr  error
	closeErr error
}

func (o *lifecycleOp) Open(ctx context.Context) error {
	*o.log = append(*o.log, fmt.Sprintf("open:%s", o.name))
	return o.openErr
}
func (o *lifecycleOp) Close() error {
	*o.log = append(*o.log, fmt.Sprintf("close:%s", o.name))
	return o.closeErr
}
func (o *lifecycleOp) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (o *lifecycleOp) Map(ctx context.Context, e Event) (Event, error) {
	return e, nil
}

// errorMap returns an error on the first Map call.
type errorMap struct {
	err error
}

func (e *errorMap) Open(ctx context.Context) error                   { return nil }
func (e *errorMap) Close() error                                     { return nil }
func (e *errorMap) Checkpoint(id uint64) ([]byte, error)             { return nil, nil }
func (e *errorMap) Map(ctx context.Context, ev Event) (Event, error) { return Event{}, e.err }

// errorFlatMap returns an error on the first FlatMap call.
type errorFlatMap struct {
	err error
}

func (e *errorFlatMap) Open(ctx context.Context) error       { return nil }
func (e *errorFlatMap) Close() error                         { return nil }
func (e *errorFlatMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (e *errorFlatMap) FlatMap(ctx context.Context, ev Event, emit func(Event)) error {
	return e.err
}

// errorSink returns an error on the first Write call.
type errorSink struct {
	err error
}

func (e *errorSink) Open(ctx context.Context) error       { return nil }
func (e *errorSink) Close() error                         { return nil }
func (e *errorSink) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (e *errorSink) Write(ctx context.Context, ev Event) error {
	return e.err
}

// -- Lifecycle tests --

func TestOperatorChain_OpenError_ClosesPreviouslyOpenedInReverse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var log []string
	op1 := &lifecycleOp{name: "op1", log: &log}
	op2 := &lifecycleOp{name: "op2", log: &log, openErr: errors.New("op2 open failed")}
	op3 := &lifecycleOp{name: "op3", log: &log}

	inputCh := make(chan Event, 1)
	close(inputCh)
	controlCh := make(chan ControlMsg, 1)
	outputCh := make(chan OutputMsg, 1)
	aligner := NewBarrierAligner(1, 100)

	err := runOperatorChain(ctx, []Operator{op1, op2, op3}, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err == nil || !errors.Is(err, op2.openErr) {
		t.Fatalf("expected op2 open error, got: %v", err)
	}

	// op1 should have been opened then closed. op3 should never have been opened.
	expected := []string{"open:op1", "open:op2", "close:op1"}
	if len(log) != len(expected) {
		t.Fatalf("lifecycle log: got %v, want %v", log, expected)
	}
	for i, e := range expected {
		if log[i] != e {
			t.Errorf("log[%d]: got %q, want %q", i, log[i], e)
		}
	}
}

func TestOperatorChain_CloseCalledInReverseOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var log []string
	op1 := &lifecycleOp{name: "op1", log: &log}
	op2 := &lifecycleOp{name: "op2", log: &log}
	op3 := &lifecycleOp{name: "op3", log: &log}

	inputCh := make(chan Event, 1)
	close(inputCh) // Closes immediately to end chain.
	controlCh := make(chan ControlMsg, 1)
	outputCh := make(chan OutputMsg, 1)
	aligner := NewBarrierAligner(1, 100)

	err := runOperatorChain(ctx, []Operator{op1, op2, op3}, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	// Close order should be reverse: op3, op2, op1.
	expected := []string{"open:op1", "open:op2", "open:op3", "close:op3", "close:op2", "close:op1"}
	if len(log) != len(expected) {
		t.Fatalf("lifecycle log: got %v, want %v", log, expected)
	}
	for i, e := range expected {
		if log[i] != e {
			t.Errorf("log[%d]: got %q, want %q", i, log[i], e)
		}
	}
}

// -- Abort checkpoint test --

func TestOperatorChain_AbortCheckpoint_DrainsSideBufferNoBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(2, 100)

	// Simulate input 0 has sent a barrier and buffered events.
	aligner.OnBarrier(0, 1, 1)
	if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("side-buffered")}); err != nil {
		t.Fatalf("BufferEvent: %v", err)
	}

	// Send abort for this checkpoint.
	controlCh <- ControlMsg{Type: CtrlAbortCheckpoint, CheckpointID: 1}
	close(inputCh)

	ops := []Operator{&noopMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	close(outputCh)
	var dataCount int
	var barrierCount int
	for msg := range outputCh {
		switch msg.Type {
		case OutputData:
			dataCount++
			if string(msg.Event.Value) != "side-buffered" {
				t.Errorf("expected side-buffered event, got %q", msg.Event.Value)
			}
		case OutputBarrier:
			barrierCount++
		}
	}

	if dataCount != 1 {
		t.Errorf("expected 1 drained event, got %d", dataCount)
	}
	if barrierCount != 0 {
		t.Errorf("expected no barrier on abort, got %d", barrierCount)
	}
	if aligner.ActiveCheckpointID() != 0 {
		t.Errorf("aligner should be reset, got active checkpoint %d", aligner.ActiveCheckpointID())
	}
}

// -- Checkpoint barrier: side buffer drained before barrier --

func TestOperatorChain_CheckpointBarrier_SideBufferDrainedBeforeBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(2, 100)

	// Simulate: input 0 barrier arrived, events side-buffered.
	aligner.OnBarrier(0, 1, 1)
	if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("side-1")}); err != nil {
		t.Fatalf("BufferEvent: %v", err)
	}
	if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("side-2")}); err != nil {
		t.Fatalf("BufferEvent: %v", err)
	}

	// Input 1 barrier arrives — alignment complete.
	aligner.OnBarrier(1, 1, 1)

	controlCh <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}
	close(inputCh)

	ops := []Operator{&noopMap{}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	close(outputCh)
	var msgs []OutputMsg
	for msg := range outputCh {
		msgs = append(msgs, msg)
	}

	// Expect: OutputData("side-1"), OutputData("side-2"), OutputBarrier.
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 output messages, got %d", len(msgs))
	}

	// First messages should be the drained side-buffer data.
	for i := 0; i < 2; i++ {
		if msgs[i].Type != OutputData {
			t.Errorf("msgs[%d]: expected OutputData, got %v", i, msgs[i].Type)
		}
	}
	if string(msgs[0].Event.Value) != "side-1" {
		t.Errorf("msgs[0]: got %q, want side-1", msgs[0].Event.Value)
	}
	if string(msgs[1].Event.Value) != "side-2" {
		t.Errorf("msgs[1]: got %q, want side-2", msgs[1].Event.Value)
	}

	// Barrier should come AFTER the drained data.
	if msgs[2].Type != OutputBarrier {
		t.Errorf("msgs[2]: expected OutputBarrier, got %v", msgs[2].Type)
	}
	if msgs[2].Barrier.CheckpointID != 1 {
		t.Errorf("barrier checkpoint ID: got %d, want 1", msgs[2].Barrier.CheckpointID)
	}
}

// -- Error propagation tests --

func TestOperatorChain_MapErrorPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	mapErr := errors.New("map failed")
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	ops := []Operator{&errorMap{err: mapErr}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, mapErr) {
		t.Fatalf("expected map error, got: %v", err)
	}
}

func TestOperatorChain_FlatMapErrorPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	fmErr := errors.New("flatmap failed")
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	ops := []Operator{&errorFlatMap{err: fmErr}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, fmErr) {
		t.Fatalf("expected flatmap error, got: %v", err)
	}
}

func TestOperatorChain_SinkErrorPropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sinkErr := errors.New("sink failed")
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	ops := []Operator{&errorSink{err: sinkErr}}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sinkErr) {
		t.Fatalf("expected sink error, got: %v", err)
	}
}

// -- EoP drains in-flight events from inputCh --

func TestOperatorChain_FinalEoPDrainsInputChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Pre-fill input channel with events.
	inputCh <- Event{Value: []byte("a")}
	inputCh <- Event{Value: []byte("b")}

	// Send the final EoP.
	controlCh <- ControlMsg{Type: CtrlEndOfPartition, InputIndex: 0}

	ops := []Operator{&noopMap{}}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOperatorChain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operator chain did not finish")
	}

	close(outputCh)
	var msgs []OutputMsg
	for msg := range outputCh {
		msgs = append(msgs, msg)
	}

	// Should have at least the two data events and the OutputEnd.
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 output messages, got %d", len(msgs))
	}

	// Last message should be OutputEnd.
	last := msgs[len(msgs)-1]
	if last.Type != OutputEnd {
		t.Errorf("last message should be OutputEnd, got %v", last.Type)
	}

	// Data events should appear before OutputEnd.
	var dataValues []string
	for _, m := range msgs {
		if m.Type == OutputData {
			dataValues = append(dataValues, string(m.Event.Value))
		}
	}
	if len(dataValues) < 2 {
		t.Errorf("expected at least 2 data events before EoP, got %d", len(dataValues))
	}
}

// -- Output backpressure + ctx cancel --

func TestOperatorChain_OutputBackpressure_UnblocksOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 1) // Tiny output channel.
	aligner := NewBarrierAligner(1, 100)

	// Pre-fill outputCh so next send blocks.
	outputCh <- OutputMsg{Type: OutputData}

	// Send an event that will try to write to the full outputCh.
	inputCh <- Event{Value: []byte("block")}

	ops := []Operator{&noopMap{}}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	}()

	// Let the chain block on output, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("operator chain did not unblock on context cancel")
	}
}

// -- Transactional sink tests (WIP-10) --

func TestOperatorChain_TransactionalSink_NormalCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{
		writeCh:     make(chan struct{}, 10),
		preCommitCh: make(chan struct{}, 1),
		commitCh:    make(chan struct{}, 1),
	}

	var acked []uint64
	var ackedMu sync.Mutex
	ackFn := func(checkpointID uint64) {
		ackedMu.Lock()
		acked = append(acked, checkpointID)
		ackedMu.Unlock()
	}

	ops := []Operator{sink}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, ackFn, nil, nil, NoopErrorMetrics())
	}()

	// Send events and wait for them to be written.
	inputCh <- Event{Value: []byte("a")}
	inputCh <- Event{Value: []byte("b")}
	for i := 0; i < 2; i++ {
		select {
		case <-sink.writeCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Write")
		}
	}

	// Now send barrier (pre-register with aligner first).
	aligner.OnBarrier(0, 1, 1)
	controlCh <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}
	select {
	case <-sink.preCommitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PreCommit")
	}

	// Send commit.
	controlCh <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 1, EpochID: 1}
	select {
	case <-sink.commitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Commit")
	}

	// Shutdown.
	controlCh <- ControlMsg{Type: CtrlShutdown}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOperatorChain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operator chain did not finish")
	}

	// Verify writes.
	written := sink.Written()
	if len(written) != 2 {
		t.Fatalf("written: got %d, want 2", len(written))
	}

	// Verify BeginTransaction called at startup + after Commit.
	if sink.BeginTxnCalls() != 2 {
		t.Errorf("BeginTransaction calls: got %d, want 2", sink.BeginTxnCalls())
	}

	// Verify PreCommit called.
	precommits := sink.PreCommitCallIDs()
	if len(precommits) != 1 || precommits[0] != 1 {
		t.Errorf("PreCommit calls: got %v, want [1]", precommits)
	}

	// Verify ackFn called.
	ackedMu.Lock()
	if len(acked) != 1 || acked[0] != 1 {
		t.Errorf("ackFn calls: got %v, want [1]", acked)
	}
	ackedMu.Unlock()

	// Verify Commit called.
	commits := sink.CommitCallIDs()
	if len(commits) != 1 || commits[0] != 1 {
		t.Errorf("Commit calls: got %v, want [1]", commits)
	}

	// Verify no barrier forwarded to outputCh (sink is terminal + transactional).
	close(outputCh)
	for msg := range outputCh {
		if msg.Type == OutputBarrier {
			t.Error("unexpected barrier in output — transactional sink should not forward barriers")
		}
	}
}

func TestOperatorChain_TransactionalSink_PreCommitError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{preCommitErr: errors.New("disk full")}

	aligner.OnBarrier(0, 1, 1)
	controlCh <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}
	close(inputCh)

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrPreCommitFailed) {
		t.Fatalf("expected ErrPreCommitFailed, got: %v", err)
	}
}

func TestOperatorChain_TransactionalSink_CommitError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{commitErr: errors.New("commit timeout")}

	controlCh <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 1, EpochID: 1}
	close(inputCh)

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("expected ErrCommitFailed, got: %v", err)
	}
}

func TestOperatorChain_TransactionalSink_AbortThenRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{}

	controlCh <- ControlMsg{Type: CtrlAbortTransaction}
	controlCh <- ControlMsg{Type: CtrlShutdown}

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	// BeginTransaction at startup + after Abort.
	if sink.BeginTxnCalls() != 2 {
		t.Errorf("BeginTransaction calls: got %d, want 2", sink.BeginTxnCalls())
	}
	if sink.AbortCallCount() != 1 {
		t.Errorf("Abort calls: got %d, want 1", sink.AbortCallCount())
	}
}

func TestOperatorChain_TransactionalSink_AbortError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{abortErr: errors.New("abort failed")}

	controlCh <- ControlMsg{Type: CtrlAbortTransaction}
	close(inputCh)

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrAbortFailed) {
		t.Fatalf("expected ErrAbortFailed, got: %v", err)
	}
}

func TestOperatorChain_NonTransactionalSink_Unchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Use regular (non-transactional) sink.
	sink := &countingSink{}
	ops := []Operator{sink}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	}()

	// Send event — the sink is terminal so no output to wait on. We rely on
	// control message ordering: the barrier is processed after the event
	// because the operator chain drains inputCh before handling controlCh.
	inputCh <- Event{Value: []byte("x")}

	// Send barrier — should be forwarded to outputCh for non-transactional sink.
	aligner.OnBarrier(0, 1, 1)
	controlCh <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}

	// Wait for the barrier to appear on outputCh (deterministic sync).
	select {
	case msg := <-outputCh:
		if msg.Type != OutputBarrier {
			t.Errorf("expected OutputBarrier, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for barrier on outputCh")
	}

	// Shutdown.
	controlCh <- ControlMsg{Type: CtrlShutdown}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOperatorChain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operator chain did not finish")
	}

	if sink.Count() != 1 {
		t.Errorf("sink count: got %d, want 1", sink.Count())
	}
}

func TestOperatorChain_TransactionalSink_IdempotentCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sink := &mockTransactionalSink{}

	// Send commit twice.
	controlCh <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 1, EpochID: 1}
	controlCh <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 1, EpochID: 1}
	controlCh <- ControlMsg{Type: CtrlShutdown}

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("runOperatorChain: %v", err)
	}

	commits := sink.CommitCallIDs()
	if len(commits) != 2 {
		t.Errorf("Commit calls: got %d, want 2", len(commits))
	}

	// BeginTransaction: 1 at startup + 2 after each commit = 3
	if sink.BeginTxnCalls() != 3 {
		t.Errorf("BeginTransaction calls: got %d, want 3", sink.BeginTxnCalls())
	}
}

func TestOperatorChain_TransactionalSink_BeginTransactionErrorAtStartup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	close(inputCh)

	sink := &mockTransactionalSink{beginTxnErr: errors.New("connection refused")}

	ops := []Operator{sink}
	err := runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrBeginTransactionFailed) {
		t.Fatalf("expected ErrBeginTransactionFailed, got: %v", err)
	}
}

// -- WIP-11: Error handling integration tests --

// transientErrorMap returns ErrTransient-wrapped errors for a configurable number of calls.
type transientErrorMap struct {
	mu        sync.Mutex
	failCount int // number of calls that will fail
	callCount int
	lastEvent Event
}

func (m *transientErrorMap) Open(ctx context.Context) error       { return nil }
func (m *transientErrorMap) Close() error                         { return nil }
func (m *transientErrorMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (m *transientErrorMap) Map(ctx context.Context, e Event) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	m.lastEvent = e
	if m.callCount <= m.failCount {
		return Event{}, fmt.Errorf("transient map error: %w", ErrTransient)
	}
	return e, nil
}
func (m *transientErrorMap) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// poisonErrorMap always returns a non-sentinel error (classified as Poison).
type poisonErrorMap struct{}

func (p *poisonErrorMap) Open(ctx context.Context) error       { return nil }
func (p *poisonErrorMap) Close() error                         { return nil }
func (p *poisonErrorMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (p *poisonErrorMap) Map(ctx context.Context, e Event) (Event, error) {
	return Event{}, errors.New("bad record format")
}

// panicOnFirstMap panics on the first call, succeeds on subsequent calls.
type panicOnFirstMap struct {
	mu    sync.Mutex
	calls int
}

func (p *panicOnFirstMap) Open(ctx context.Context) error       { return nil }
func (p *panicOnFirstMap) Close() error                         { return nil }
func (p *panicOnFirstMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (p *panicOnFirstMap) Map(ctx context.Context, e Event) (Event, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == 1 {
		panic("deserialization panic")
	}
	return e, nil
}

func TestOperatorChain_ErrorHandler_MapRetryThenSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Map fails twice then succeeds.
	op := &transientErrorMap{failCount: 2}

	inputCh <- Event{Value: []byte("x")}
	close(inputCh)

	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "retry-map",
		MaxRetries:   5,
		Backoff:      FixedBackoff(1 * time.Millisecond),
		OnExhausted:  FailJob,
	}}

	err := runOperatorChain(ctx, []Operator{op}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}

	close(outputCh)
	var got int
	for msg := range outputCh {
		if msg.Type == OutputData {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("got %d events, want 1", got)
	}
	if op.CallCount() != 3 {
		t.Errorf("call count: got %d, want 3 (1 initial + 2 retries + 1 success on 3rd)", op.CallCount())
	}
}

func TestOperatorChain_ErrorHandler_MapPoisonToDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// Send 3 events: 1 will poison, 2 will pass (we chain poisonErrorMap with noopMap
	// at different indices). For this test, use a single poisonErrorMap that always fails.
	// The good events are processed by a different chain.
	//
	// Simpler: send 1 event through a poison map → should route to DLQ, not fail job.
	inputCh <- Event{Value: []byte("bad")}
	close(inputCh)

	dlqCh := make(chan DLQEvent, 10)
	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "poison-map",
		MaxRetries:   0,
		OnExhausted:  RouteToDLQ,
	}}

	err := runOperatorChain(ctx, []Operator{&poisonErrorMap{}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected nil (event DLQ'd), got: %v", err)
	}

	// No output events (event was DLQ'd).
	close(outputCh)
	for msg := range outputCh {
		t.Errorf("unexpected output: %v", msg)
	}

	// Verify DLQ event.
	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
	dlqEvent := <-dlqCh
	if dlqEvent.OperatorName != "poison-map" {
		t.Errorf("DLQ operator: got %q, want %q", dlqEvent.OperatorName, "poison-map")
	}
	if string(dlqEvent.OriginalEvent.Value) != "bad" {
		t.Errorf("DLQ event value: got %q, want %q", dlqEvent.OriginalEvent.Value, "bad")
	}
}

func TestOperatorChain_ErrorHandler_PanicRoutesDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	inputCh <- Event{Value: []byte("panic-trigger")}
	close(inputCh)

	dlqCh := make(chan DLQEvent, 10)
	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "panic-map",
		MaxRetries:   0,
		OnExhausted:  RouteToDLQ,
	}}

	err := runOperatorChain(ctx, []Operator{&panicOnFirstMap{}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected nil (panic caught and DLQ'd), got: %v", err)
	}

	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
	dlqEvent := <-dlqCh
	if dlqEvent.OperatorName != "panic-map" {
		t.Errorf("DLQ operator: got %q, want %q", dlqEvent.OperatorName, "panic-map")
	}
}

func TestOperatorChain_ErrorHandler_SinkRetryExhausted_FailsJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	sinkErr := fmt.Errorf("sink write failed: %w", ErrTransient)
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "fail-sink",
		MaxRetries:   2,
		Backoff:      FixedBackoff(1 * time.Millisecond),
		OnExhausted:  FailJob,
	}}

	err := runOperatorChain(ctx, []Operator{&errorSink{err: sinkErr}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrRetriesExhausted) {
		t.Fatalf("expected ErrRetriesExhausted, got: %v", err)
	}
}

func TestOperatorChain_ErrorHandler_NilConfigs_LegacyBehavior(t *testing.T) {
	// nil errorConfigs = legacy behavior: any error fails the job.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	mapErr := errors.New("map failed")
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	// nil errorConfigs, nil dlqCh.
	err := runOperatorChain(ctx, []Operator{&errorMap{err: mapErr}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, mapErr) {
		t.Fatalf("expected map error (legacy behavior), got: %v", err)
	}
}

func TestOperatorChain_ErrorHandler_FlatMapRetryDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	fmErr := fmt.Errorf("flatmap transient: %w", ErrTransient)
	inputCh <- Event{Value: []byte("trigger")}
	close(inputCh)

	dlqCh := make(chan DLQEvent, 10)
	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "fail-flatmap",
		MaxRetries:   1,
		Backoff:      FixedBackoff(1 * time.Millisecond),
		OnExhausted:  RouteToDLQ,
	}}

	err := runOperatorChain(ctx, []Operator{&errorFlatMap{err: fmErr}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected nil (DLQ'd), got: %v", err)
	}

	if len(dlqCh) != 1 {
		t.Fatalf("expected 1 DLQ event, got %d", len(dlqCh))
	}
}

func TestOperatorChain_ErrorHandler_DropEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	inputCh <- Event{Value: []byte("drop-me")}
	close(inputCh)

	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "drop-map",
		MaxRetries:   0,
		OnExhausted:  DropEvent,
	}}

	err := runOperatorChain(ctx, []Operator{&poisonErrorMap{}}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, nil, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected nil (dropped), got: %v", err)
	}

	close(outputCh)
	for msg := range outputCh {
		t.Errorf("unexpected output (should have been dropped): %v", msg)
	}
}

func TestOperatorChain_ErrorHandler_MixedGoodAndBadEvents(t *testing.T) {
	// Send 100 events through a map that fails on event #50 (poison).
	// With DLQ configured, 99 events should pass through and 1 goes to DLQ.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 200)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 200)
	aligner := NewBarrierAligner(1, 100)

	for i := 0; i < 100; i++ {
		inputCh <- Event{Value: []byte{byte(i)}, EventTime: int64(i)}
	}
	close(inputCh)

	dlqCh := make(chan DLQEvent, 10)
	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "selective-map",
		MaxRetries:   0,
		OnExhausted:  RouteToDLQ,
	}}

	// Map that fails only on event #50.
	selectiveMap := &selectiveErrorMap{failIndex: 50}
	err := runOperatorChain(ctx, []Operator{selectiveMap}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}

	close(outputCh)
	var dataCount int
	for msg := range outputCh {
		if msg.Type == OutputData {
			dataCount++
		}
	}
	if dataCount != 99 {
		t.Errorf("data events: got %d, want 99", dataCount)
	}
	if len(dlqCh) != 1 {
		t.Errorf("DLQ events: got %d, want 1", len(dlqCh))
	}
}

func TestOperatorChain_ErrorHandler_WithTransactionalSink(t *testing.T) {
	// Verify that error handling on a map operator works alongside
	// a transactional sink (2PC protocol is unaffected).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(1, 100)

	// transientErrorMap that fails first 2 calls then succeeds.
	mapOp := &transientErrorMap{failCount: 2}
	sink := &mockTransactionalSink{
		writeCh:     make(chan struct{}, 10),
		preCommitCh: make(chan struct{}, 1),
		commitCh:    make(chan struct{}, 1),
	}

	ops := []Operator{mapOp, sink}

	// Error config only on the map operator (index 0). Sink (index 1) has zero config.
	errorConfigs := []ErrorHandlerConfig{
		{
			OperatorName: "retry-map",
			MaxRetries:   5,
			Backoff:      FixedBackoff(1 * time.Millisecond),
			OnExhausted:  FailJob,
		},
		{}, // Zero config for sink.
	}

	var acked []uint64
	var ackedMu sync.Mutex
	ackFn := func(checkpointID uint64) {
		ackedMu.Lock()
		acked = append(acked, checkpointID)
		ackedMu.Unlock()
	}

	done := make(chan error, 1)
	go func() {
		done <- runOperatorChain(ctx, ops, inputCh, controlCh, outputCh, aligner, 1,
			NoopCheckpointMetrics(), testLogger(), sink, ackFn, errorConfigs, nil, NoopErrorMetrics())
	}()

	// Send event and wait for it to be written (after retries).
	inputCh <- Event{Value: []byte("a")}
	select {
	case <-sink.writeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Write")
	}

	// Send barrier and wait for PreCommit.
	aligner.OnBarrier(0, 1, 1)
	controlCh <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 1, EpochID: 1}
	select {
	case <-sink.preCommitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PreCommit")
	}

	// Send commit and wait for Commit.
	controlCh <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 1, EpochID: 1}
	select {
	case <-sink.commitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Commit")
	}

	controlCh <- ControlMsg{Type: CtrlShutdown}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runOperatorChain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operator chain did not finish")
	}

	// Verify the sink received the event (after retries).
	if len(sink.Written()) != 1 {
		t.Errorf("written: got %d, want 1", len(sink.Written()))
	}
	// Verify 2PC lifecycle.
	if sink.BeginTxnCalls() != 2 { // startup + after commit
		t.Errorf("BeginTransaction calls: got %d, want 2", sink.BeginTxnCalls())
	}
	ackedMu.Lock()
	if len(acked) != 1 || acked[0] != 1 {
		t.Errorf("ackFn calls: got %v, want [1]", acked)
	}
	ackedMu.Unlock()
}

// T1: Classifier-only config activates error handling path.
// A user can set Classifier without MaxRetries or OnExhausted; the custom
// classifier must still be invoked (not silently ignored via legacy path).
func TestOperatorChain_ClassifierOnlyConfig_ActivatesErrorHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inputCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	outputCh := make(chan OutputMsg, 10)
	dlqCh := make(chan DLQEvent, 10)
	aligner := NewBarrierAligner(1, 100)

	// Operator always returns an error. Custom classifier marks it Transient
	// (but MaxRetries=0, OnExhausted=FailJob, so it will exhaust immediately
	// and fail the job). The key test: the custom classifier IS invoked.
	classifierCalled := false
	errorConfigs := []ErrorHandlerConfig{{
		OperatorName: "classifier-only",
		MaxRetries:   0,
		OnExhausted:  FailJob, // zero value
		Classifier: func(err error) ErrorClass {
			classifierCalled = true
			return ErrorClassPoison
		},
	}}

	op := &poisonErrorMap{}
	inputCh <- Event{Value: []byte("x")}
	close(inputCh)

	err := runOperatorChain(ctx, []Operator{op}, inputCh, controlCh, outputCh, aligner, 1,
		NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())

	// With Classifier-only config (MaxRetries=0, OnExhausted=FailJob), a poison
	// error goes through handleExhausted → FailJob → returns error.
	if err == nil {
		t.Fatal("expected error (FailJob on poison), got nil")
	}
	if !classifierCalled {
		t.Error("custom classifier was never called — hasErrorHandling guard is ignoring Classifier field")
	}
}

// T2: Panic behavior differs between legacy and error-handling paths.
// Legacy path: panic propagates to top-level defer recover → ErrOperatorPanic.
// Error-handling path: safeInvoke catches panic → classified as Poison → DLQ'd.
func TestOperatorChain_PanicBehavior_LegacyVsErrorHandling(t *testing.T) {
	t.Run("legacy_path_panic_fails_job", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		inputCh := make(chan Event, 10)
		controlCh := make(chan ControlMsg, 10)
		outputCh := make(chan OutputMsg, 10)
		aligner := NewBarrierAligner(1, 100)

		op := &panicOnFirstMap{}
		inputCh <- Event{Value: []byte("x")}
		close(inputCh)

		// No error configs → legacy path.
		err := runOperatorChain(ctx, []Operator{op}, inputCh, controlCh, outputCh, aligner, 1,
			NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics())

		if !errors.Is(err, ErrOperatorPanic) {
			t.Fatalf("legacy path: expected ErrOperatorPanic, got: %v", err)
		}
	})

	t.Run("error_handling_path_panic_dlqs", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		inputCh := make(chan Event, 10)
		controlCh := make(chan ControlMsg, 10)
		outputCh := make(chan OutputMsg, 10)
		dlqCh := make(chan DLQEvent, 10)
		aligner := NewBarrierAligner(1, 100)

		op := &panicOnFirstMap{}
		inputCh <- Event{Value: []byte("x")}
		close(inputCh)

		// Error handling enabled → safeInvoke catches panic → Poison → DLQ.
		errorConfigs := []ErrorHandlerConfig{{
			OperatorName: "panic-op",
			MaxRetries:   2,
			OnExhausted:  RouteToDLQ,
		}}

		err := runOperatorChain(ctx, []Operator{op}, inputCh, controlCh, outputCh, aligner, 1,
			NoopCheckpointMetrics(), testLogger(), nil, nil, errorConfigs, dlqCh, NoopErrorMetrics())

		if err != nil {
			t.Fatalf("error-handling path: expected nil (DLQ'd), got: %v", err)
		}
		if len(dlqCh) != 1 {
			t.Fatalf("expected 1 DLQ event (panic caught), got %d", len(dlqCh))
		}
		dlqEvent := <-dlqCh
		if dlqEvent.OperatorName != "panic-op" {
			t.Errorf("DLQ operator: got %q, want %q", dlqEvent.OperatorName, "panic-op")
		}
	})
}

// selectiveErrorMap fails on a specific event index (0-based).
type selectiveErrorMap struct {
	mu        sync.Mutex
	count     int
	failIndex int
}

func (m *selectiveErrorMap) Open(ctx context.Context) error       { return nil }
func (m *selectiveErrorMap) Close() error                         { return nil }
func (m *selectiveErrorMap) Checkpoint(id uint64) ([]byte, error) { return nil, nil }
func (m *selectiveErrorMap) Map(ctx context.Context, e Event) (Event, error) {
	m.mu.Lock()
	idx := m.count
	m.count++
	m.mu.Unlock()
	if idx == m.failIndex {
		return Event{}, errors.New("selective failure")
	}
	return e, nil
}
