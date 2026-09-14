package engine

import (
	"context"
	"testing"
	"time"
)

func TestOperatorChainCheckpointControlWithFullDataChannel(t *testing.T) {
	for _, abort := range []bool{false, true} {
		name := "checkpoint"
		if abort {
			name = "abort"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			input := make(chan Event, 100)
			for i := 0; i < cap(input); i++ {
				input <- Event{Value: []byte("queued-data")}
			}
			// Both mailboxes are ready before the chain starts. Closing a full
			// channel keeps every queued event available while allowing a join.
			close(input)
			control := make(chan ControlMsg, 1)
			output := make(chan OutputMsg, cap(input)+4)
			aligner := NewBarrierAligner(2, 10)
			aligner.OnBarrier(0, 1, 1)
			if err := aligner.BufferEvent(ctx, 0, Event{Value: []byte("buffered")}); err != nil {
				t.Fatal(err)
			}
			kind := CtrlBarrierReceived
			if abort {
				kind = CtrlAbortCheckpoint
			} else {
				aligner.OnBarrier(1, 1, 1)
			}
			control <- ControlMsg{Type: kind, CheckpointID: 1, EpochID: 1}
			if err := runOperatorChain(ctx, []Operator{&noopMap{}}, input, control, output, aligner, 2, NoopCheckpointMetrics(), testLogger(), nil, nil, nil, nil, NoopErrorMetrics()); err != nil {
				t.Fatal(err)
			}
			close(output)
			first := <-output
			if abort {
				for i := 0; i < cap(input); i++ {
					if first.Type != OutputData || string(first.Event.Value) != "queued-data" {
						t.Fatalf("abort reordered pre-barrier record %d: %+v", i, first)
					}
					first = <-output
				}
				if first.Type != OutputData || string(first.Event.Value) != "buffered" {
					t.Fatalf("abort lost post-barrier record: %+v", first)
				}
			} else {
				// Checkpoint control must drain queued pre-barrier records
				// before snapshotting, then forward the barrier before releasing
				// post-barrier data. Priority must not violate that boundary.
				for i := 0; i < cap(input); i++ {
					if first.Type != OutputData || string(first.Event.Value) != "queued-data" {
						t.Fatalf("pre-barrier record %d: %+v", i, first)
					}
					first = <-output
				}
				if first.Type != OutputBarrier || first.Barrier == nil || first.Barrier.CheckpointID != 1 {
					t.Fatalf("checkpoint not handled at the drained boundary: %+v", first)
				}
				if next := <-output; next.Type != OutputData || string(next.Event.Value) != "buffered" {
					t.Fatalf("post-barrier record: %+v", next)
				}
			}
		})
	}
}
