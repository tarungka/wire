package engine

import (
	"context"
	"math"
	"testing"
)

func TestTerminalInputWatermarkRunsBeforeNextTick(t *testing.T) {
	tracker := NewInputWatermarkTracker(2)
	output := make(chan OutputMsg, 4)
	probe := &watermarkProbe{}
	cc := &chainContext{ctx: t.Context(), links: []ChainLink{{Operator: probe}}, outputCh: output}
	for input := 0; input < 2; input++ {
		if err := processEvent(cc, Event{inputWatermark: &inputWatermarkBoundary{tracker: tracker, input: input, timestamp: math.MaxInt64}}); err != nil {
			t.Fatal(err)
		}
		if input == 0 && len(probe.seen) != 0 {
			t.Fatal("terminal boundary advanced before all inputs finished")
		}
	}
	if len(probe.seen) != 1 || probe.seen[0] != math.MaxInt64 {
		t.Fatal("terminal callback deferred until a periodic tick")
	}
	// An already-queued periodic minimum must not regress terminal event time.
	if err := processEvent(cc, WatermarkEvent(10)); err != nil {
		t.Fatal(err)
	}
	if len(probe.seen) != 1 || len(output) != 2 {
		t.Fatal("stale periodic watermark reached operators")
	}
	if result, boundary := <-output, <-output; result.Type != OutputData || boundary.Type != OutputWatermark || boundary.Watermark.Timestamp != math.MaxInt64 {
		t.Fatal("timer output must precede terminal boundary")
	}
}

type watermarkProbe struct {
	noopMap
	seen []int64
}

func (p *watermarkProbe) OnWatermark(_ context.Context, timestamp int64) ([]Event, error) {
	p.seen = append(p.seen, timestamp)
	return []Event{{Value: []byte("window")}}, nil
}

func TestOrderedWatermarkEmitsWindowResultsBeforeBoundary(t *testing.T) {
	output := make(chan OutputMsg, 3)
	probe := &watermarkProbe{}
	cc := &chainContext{ctx: context.Background(), links: []ChainLink{{Operator: probe}}, outputCh: output}
	if err := processEvent(cc, Event{Value: []byte("record")}); err != nil {
		t.Fatal(err)
	}
	timestamp := int64(100)
	if err := processEvent(cc, Event{watermark: &timestamp}); err != nil {
		t.Fatal(err)
	}
	first, second, third := <-output, <-output, <-output
	if first.Type != OutputData || string(first.Event.Value) != "record" || second.Type != OutputData || string(second.Event.Value) != "window" || third.Type != OutputWatermark || third.Watermark.Timestamp != 100 {
		t.Fatal("records, window output and watermark were reordered")
	}
	if len(probe.seen) != 1 || probe.seen[0] != 100 {
		t.Fatal("watermark callback missing")
	}
}
