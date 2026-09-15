package engine

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
)

func TestNegativeWatermarkClosesWindow(t *testing.T) {
	op, err := NewEventTimeWindowOperator(WindowConfig{Kind: "tumbling", Size: 10, AggregationID: "count"}, windowCount{}, func(result WindowResult) Event {
		return Event{Value: result.Value, EventTime: result.WindowEnd}
	})
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan OutputMsg, 3)
	chain := &chainContext{ctx: context.Background(), links: []ChainLink{{Operator: op}}, outputCh: output}
	strategy := NewMonotonicTimestampsStrategy()
	for _, ts := range []int64{-18, -12, -10} {
		if err := processEvent(chain, Event{EventTime: ts}); err != nil {
			t.Fatal(err)
		}
		strategy.ObserveEventTime(ts)
	}
	events := make(chan Event, 1)
	queue := &sourceWatermarkQueue{}
	last := int64(math.MinInt64)
	if err := queue.emit(context.Background(), strategy, events, &last); err != nil {
		t.Fatal(err)
	}
	if err := processEvent(chain, <-events); err != nil {
		t.Fatal(err)
	}
	result := <-output
	if result.Type != OutputData || result.Event.EventTime != -10 || binary.BigEndian.Uint64(result.Event.Value) != 2 {
		t.Fatalf("incorrect pre-epoch window: %+v", result)
	}
}
