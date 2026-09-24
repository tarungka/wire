package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

func TestNetworkWatermarkWaitsForOperatorQueue(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events := make(chan Event, 4)
	controls := make(chan ControlMsg, 4)
	tracker := NewInputWatermarkTracker(1)
	tracker.ordered = true
	done := make(chan error, 1)
	go func() {
		done <- runInputReader(ctx, 0, reader, events, controls, NewBarrierAligner(1, 8), tracker, zerolog.Nop())
	}()
	for _, message := range []any{&protocol.DataRecordMsg{EventTime: 10, Value: []byte("before")}, &protocol.WatermarkMsg{Timestamp: 20}, &protocol.EndOfPartitionMsg{}} {
		if err := writer.WriteMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if minimum, _ := tracker.MinWatermark(0); minimum != math.MinInt64 {
		t.Fatal("read-ahead advanced tracker before operator processing")
	}
	output := make(chan OutputMsg, 2)
	chain := &chainContext{ctx: ctx, outputCh: output}
	first := <-events
	if first.inputWatermark != nil {
		t.Fatal("watermark overtook record")
	}
	if err := processEvent(chain, first); err != nil {
		t.Fatal(err)
	}
	boundary := <-events
	if boundary.inputWatermark == nil {
		t.Fatal("missing ordered boundary")
	}
	if err := processEvent(chain, boundary); err != nil {
		t.Fatal(err)
	}
	if minimum, _ := tracker.MinWatermark(0); minimum != 20 {
		t.Fatal("processed boundary did not advance tracker")
	}
}
