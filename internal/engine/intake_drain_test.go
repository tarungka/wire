package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestInputReaderDrainsReadAheadAfterIntakeCancellation(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	intake, stop := context.WithCancel(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events := make(chan Event)
	controls := make(chan ControlMsg, 8)
	full := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- runInputReaderWithContexts(intake, ctx, 0, reader, events, controls, NewBarrierAligner(1, 10), testTracker(1), testLogger(), func(used, capacity int) error {
			if used == capacity {
				once.Do(func() { close(full) })
			}
			return nil
		})
	}()
	// One record is held by the consumer, and five occupy read-ahead slots.
	for i := 0; i < 6; i++ {
		if err := writer.WriteMessage(&protocol.DataRecordMsg{Value: []byte{byte(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-full:
	case <-ctx.Done():
		t.Fatal("read-ahead did not fill")
	}
	stop()
	for i := 0; i < 6; i++ {
		select {
		case event := <-events:
			if len(event.Value) != 1 || event.Value[0] != byte(i) {
				t.Fatalf("record %d changed: %v", i, event.Value)
			}
		case <-ctx.Done():
			t.Fatalf("record %d lost on intake cancellation", i)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("reader did not join")
	}
}

func TestSourceReaderDrainsFetchedBatchAfterIntakeCancellation(t *testing.T) {
	intake, stop := context.WithCancel(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := newMockSource([][]Event{{{Value: []byte{0}}, {Value: []byte{1}}, {Value: []byte{2}}}})
	events := make(chan Event)
	controls := make(chan ControlMsg, 4)
	done := make(chan error, 1)
	go func() { done <- runSourceReaderWithContexts(intake, ctx, source, nil, events, controls, testLogger()) }()
	select {
	case <-events:
	case <-ctx.Done():
		t.Fatal("source did not fetch batch")
	}
	stop()
	for i := 1; i < 3; i++ {
		select {
		case event := <-events:
			if event.Value[0] != byte(i) {
				t.Fatal("batch order changed")
			}
		case <-ctx.Done():
			t.Fatal("fetched batch was discarded")
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("reader: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("source did not stop")
	}
}

type intakeDrainMap struct {
	noopMap
	entered chan struct{}
	release chan struct{}
	count   int
}

func (m *intakeDrainMap) Map(ctx context.Context, event Event) (Event, error) {
	if m.count == 0 {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
			return Event{}, ctx.Err()
		}
	}
	m.count++
	return event, nil
}
func TestTaskSlotCancellationDrainsFetchedInputBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op := &intakeDrainMap{entered: make(chan struct{}), release: make(chan struct{})}
	source := newMockSource([][]Event{{{Value: []byte{0}}, {Value: []byte{1}}, {Value: []byte{2}}, {Value: []byte{3}}, {Value: []byte{4}}, {Value: []byte{5}}}})
	cfg := DefaultTaskSlotConfig()
	cfg.InputBufferSize = 2
	cfg.DrainTimeout = time.Second
	slot := NewTaskSlot(cfg, nil, nil, []Operator{op}, source)
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	select {
	case <-op.entered:
	case <-time.After(time.Second):
		t.Fatal("map did not start")
	}
	cancel()
	close(op.release)
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drain did not finish")
	}
	if op.count != 6 {
		t.Fatalf("processed %d of 6 fetched records", op.count)
	}
}

func TestDrainReleasesAlignmentWithoutReordering(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	aligner := NewBarrierAligner(2, 1)
	aligner.OnBarrier(0, 7, 7)
	if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("post-1")}); err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 2)
	events <- Event{Value: []byte("pre")}
	controls := make(chan ControlMsg, 2)
	controls <- ControlMsg{Type: CtrlDrainInputs}
	output := make(chan OutputMsg, 8)
	reader := make(chan error, 1)
	go func() {
		event := Event{Value: []byte("post-2")}
		buffered, err := aligner.BufferAlignedEvent(ctx, 0, event)
		if err == nil && !buffered {
			select {
			case events <- event:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			select {
			case controls <- ControlMsg{Type: CtrlShutdown}:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		reader <- err
	}()
	if err := runOperatorChain(ctx, []Operator{&noopMap{}}, events, controls, output, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics()); err != nil {
		t.Fatal(err)
	}
	if err := <-reader; err != nil {
		t.Fatal(err)
	}
	close(output)
	var values []string
	for message := range output {
		if message.Type != OutputData {
			t.Fatalf("unexpected control during cancellation: %+v", message)
		}
		values = append(values, string(message.Event.Value))
	}
	if !reflect.DeepEqual(values, []string{"pre", "post-1", "post-2"}) {
		t.Fatalf("drain order: %v", values)
	}
	if aligner.OnBarrier(0, 8, 8) || aligner.ActiveCheckpointID() != 0 {
		t.Fatal("alignment restarted after shutdown began")
	}
}
