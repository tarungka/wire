package engine

import (
	"context"
	"testing"
	"time"
)

func TestAlignedRecordsPreventIdleWatermarkAdvance(t *testing.T) {
	now := int64(0)
	tracker := newInputWatermarkTracker(2, func() int64 { return now })
	tracker.AdvanceWatermark(0, 10)
	tracker.AdvanceWatermark(1, 100)
	tracker.recordQueued(0)
	aligner := NewBarrierAligner(2, 4)
	aligner.OnBarrier(0, 1, 1)
	event := Event{Value: []byte("waiting"), inputActivity: &inputActivity{tracker: tracker, input: 0}}
	if buffered, err := aligner.BufferAlignedEvent(context.Background(), 0, event); !buffered || err != nil {
		t.Fatalf("buffered=%v err=%v", buffered, err)
	}
	now = int64(2 * time.Second)
	tracker.RecordActivity(1)
	if wm, idle := tracker.MinWatermark(time.Second); idle || wm != 10 {
		t.Fatalf("aligned input excluded: watermark=%d idle=%v", wm, idle)
	}
	output := make(chan OutputMsg, 1)
	cc := &chainContext{ctx: context.Background(), outputCh: output}
	for _, event := range aligner.FinishAlignment(1) {
		if err := processEvent(cc, event); err != nil {
			t.Fatal(err)
		}
	}
	if got := <-output; got.Event.inputActivity != nil {
		t.Fatal("input tracking escaped into downstream output")
	}
	if tracker.pending[0].Load() != 0 {
		t.Fatal("processing did not release pending record")
	}
	if wm, idle := tracker.MinWatermark(time.Second); idle || wm != 10 {
		t.Fatalf("idle timeout did not restart after processing: %d %v", wm, idle)
	}
	now += int64(time.Second)
	tracker.RecordActivity(1)
	if wm, idle := tracker.MinWatermark(time.Second); idle || wm != 100 {
		t.Fatalf("drained input failed to become idle: %d %v", wm, idle)
	}
}
