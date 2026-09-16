package engine

import (
	"context"
	"testing"
)

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
