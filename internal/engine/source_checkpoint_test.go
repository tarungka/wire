package engine

import (
	"context"
	"testing"
	"time"
)

type checkpointBoundarySource struct{ *mockSource }

func (s *checkpointBoundarySource) Checkpoint(uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []byte{byte(s.batchIdx)}, nil
}

func TestSourceCheckpointWaitsAtBatchBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := &checkpointBoundarySource{newMockSource([][]Event{{{Value: []byte{1}}, {Value: []byte{2}}}, {{Value: []byte{3}}}})}
	requests := make(chan CheckpointTrigger, 1)
	events := make(chan Event)
	controls := make(chan ControlMsg, 2)
	aligner := NewBarrierAligner(1, 2)
	checkpoint := &sourceCheckpointInput{requests: requests, source: source, aligner: aligner, control: controls}
	done := make(chan error, 1)
	go func() {
		done <- runSourceReaderWithContexts(ctx, ctx, source, nil, events, controls, testLogger(), checkpoint)
	}()
	receive := func(want byte) {
		t.Helper()
		select {
		case event := <-events:
			if event.Value[0] != want {
				t.Fatalf("got %v want %d", event, want)
			}
		case <-ctx.Done():
			t.Fatal("event missing")
		}
	}
	receive(1)
	requests <- CheckpointTrigger{CheckpointID: 7, EpochID: 2}
	receive(2)
	var control ControlMsg
	select {
	case control = <-controls:
	case <-ctx.Done():
		t.Fatal("boundary missing")
	}
	if control.sourceBoundary == nil || len(control.sourceBoundary.state) != 1 || control.sourceBoundary.state[0] != 1 {
		t.Fatalf("wrong source state: %+v", control)
	}
	if !aligner.AllAligned(7) || aligner.ActiveEpochID() != 2 {
		t.Fatal("source barrier identity missing")
	}
	source.mu.Lock()
	reads := source.batchIdx
	source.mu.Unlock()
	if reads != 1 {
		t.Fatalf("advanced beyond snapshot: %d", reads)
	}
	select {
	case event := <-events:
		t.Fatalf("post-barrier event escaped: %v", event)
	default:
	}
	aligner.FinishAlignment(7)
	close(control.sourceBoundary.done)
	receive(3)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("reader did not finish")
	}
}

func TestSourceCheckpointIgnoresRedeliveredIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := &checkpointBoundarySource{newMockSource(nil)}
	requests := make(chan CheckpointTrigger, 1)
	controls := make(chan ControlMsg, 1)
	aligner := NewBarrierAligner(1, 2)
	input := &sourceCheckpointInput{requests: requests, source: source, aligner: aligner, control: controls}
	requests <- CheckpointTrigger{CheckpointID: 7, EpochID: 2}
	done := make(chan error, 1)
	go func() { done <- input.atBoundary(ctx, ctx) }()
	var boundary ControlMsg
	select {
	case boundary = <-controls:
	case <-ctx.Done():
		t.Fatal("missing first boundary")
	}
	aligner.FinishAlignment(7)
	close(boundary.sourceBoundary.done)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A duplicate after processing advances must not overwrite the original
	// snapshot or emit another barrier, even when it carries a larger old-epoch ID.
	source.mu.Lock()
	source.batchIdx = 1
	source.mu.Unlock()
	for _, request := range []CheckpointTrigger{{7, 2}, {6, 2}, {99, 1}} {
		requests <- request
		if err := input.atBoundary(ctx, ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case <-controls:
			t.Fatal("redelivery emitted a barrier")
		default:
		}
		if aligner.ActiveCheckpointID() != 0 {
			t.Fatal("redelivery restarted alignment")
		}
	}
}
