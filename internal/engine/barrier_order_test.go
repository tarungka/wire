package engine

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

type snapshotCountingMap struct {
	noopMap
	processed     int
	snapshotCount int
}

func (m *snapshotCountingMap) Map(_ context.Context, event Event) (Event, error) {
	m.processed++
	return event, nil
}
func (m *snapshotCountingMap) Checkpoint(uint64) ([]byte, error) {
	m.snapshotCount = m.processed
	return nil, nil
}

func TestAlignedBarrierPreservesQueuedAndBufferedRecordOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	events := make(chan Event, 2)
	events <- Event{Value: []byte("pre-a")}
	events <- Event{Value: []byte("pre-b")}
	close(events)
	controls := make(chan ControlMsg, 3)
	output := make(chan OutputMsg, 10)
	aligner := NewBarrierAligner(2, 10)
	aligner.OnBarrier(0, 7, 4)
	aligner.OnBarrier(1, 7, 4)
	if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("post-a")}); err != nil {
		t.Fatal(err)
	}
	if err := aligner.BufferEvent(ctx, 1, Event{Value: []byte("post-b")}); err != nil {
		t.Fatal(err)
	}
	controls <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 7, EpochID: 4}
	controls <- ControlMsg{Type: CtrlEndOfPartition, InputIndex: 0}
	controls <- ControlMsg{Type: CtrlEndOfPartition, InputIndex: 1}
	op := &snapshotCountingMap{}
	if err := runOperatorChain(ctx, []Operator{op}, events, controls, output, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics()); err != nil {
		t.Fatal(err)
	}
	if op.snapshotCount != 2 {
		t.Errorf("snapshot included %d records, want two pre-barrier records", op.snapshotCount)
	}
	close(output)
	var got []string
	for message := range output {
		switch message.Type {
		case OutputData:
			got = append(got, string(message.Event.Value))
		case OutputBarrier:
			got = append(got, "barrier")
		case OutputEnd:
			got = append(got, "end")
		}
	}
	want := []string{"pre-a", "pre-b", "barrier", "post-a", "post-b", "end"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output %v, want %v", got, want)
	}
}

func TestFinishAlignmentPreservesConcurrentEvents(t *testing.T) {
	ctx := context.Background()
	aligner := NewBarrierAligner(1, 128)
	aligner.OnBarrier(0, 7, 7)
	normal := make(chan Event, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			event := Event{Value: []byte{byte(id)}}
			buffered, err := aligner.BufferAlignedEvent(ctx, 0, event)
			if err != nil {
				t.Error(err)
				return
			}
			if !buffered {
				normal <- event
			}
		}(i)
	}
	transferred := aligner.FinishAlignment(7)
	wg.Wait()
	close(normal)
	for event := range normal {
		transferred = append(transferred, event)
	}
	seen := make(map[byte]bool)
	for _, event := range transferred {
		if seen[event.Value[0]] {
			t.Fatalf("duplicate event %d", event.Value[0])
		}
		seen[event.Value[0]] = true
	}
	if len(seen) != 64 {
		t.Fatalf("retained %d of 64 events", len(seen))
	}
}

func TestFullAlignmentBufferWakesOnReset(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	aligner := NewBarrierAligner(1, 1)
	aligner.OnBarrier(0, 7, 7)
	if buffered, err := aligner.BufferAlignedEvent(ctx, 0, Event{Value: []byte("first")}); !buffered || err != nil {
		t.Fatalf("first: %v %v", buffered, err)
	}
	result := make(chan bool, 1)
	go func() {
		buffered, err := aligner.BufferAlignedEvent(ctx, 0, Event{Value: []byte("next")})
		if err != nil {
			t.Error(err)
		}
		result <- buffered
	}()
	select {
	case <-result:
		t.Fatal("full buffer did not wait")
	case <-time.After(10 * time.Millisecond):
	}
	events := aligner.FinishAlignment(7)
	if len(events) != 1 || string(events[0].Value) != "first" {
		t.Fatal("buffer transfer changed")
	}
	select {
	case buffered := <-result:
		if buffered {
			t.Fatal("post-reset record orphaned in side buffer")
		}
	case <-ctx.Done():
		t.Fatal("reset did not wake reader")
	}
}

func TestNextBarrierWaitsForPriorAlignment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	aligner := NewBarrierAligner(1, 1)
	aligner.OnBarrier(0, 7, 7)
	result := make(chan error, 1)
	go func() { result <- aligner.WaitForPriorAlignment(ctx, 0, 8) }()
	select {
	case <-result:
		t.Fatal("next barrier passed unfinished alignment")
	case <-time.After(10 * time.Millisecond):
	}
	aligner.FinishAlignment(7)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("next barrier did not wake")
	}
	if !aligner.OnBarrier(0, 8, 8) {
		t.Fatal("next barrier was lost")
	}
}
