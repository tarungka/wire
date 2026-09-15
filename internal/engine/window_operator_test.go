package engine

import (
	"context"
	"encoding/binary"
	"testing"
)

func TestWindowOperatorOrderedClosureAndRecovery(t *testing.T) {
	config := WindowConfig{Kind: "tumbling", Size: 10, AggregationID: "count-v1"}
	build := func() *EventTimeWindowOperator {
		op, err := NewEventTimeWindowOperator(config, windowCount{}, func(result WindowResult) Event {
			return Event{Key: result.Key, Value: result.Value, EventTime: result.WindowEnd}
		})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	op := build()
	output := make(chan OutputMsg, 8)
	chain := &chainContext{ctx: context.Background(), links: []ChainLink{{Operator: op}}, outputCh: output}
	for _, timestamp := range []int64{8, 2} {
		if err := processEvent(chain, Event{EventTime: timestamp}); err != nil {
			t.Fatal(err)
		}
	}
	watermark := int64(10)
	if err := processEvent(chain, Event{watermark: &watermark}); err != nil {
		t.Fatal(err)
	}
	result := <-output
	if result.Type != OutputData || binary.BigEndian.Uint64(result.Event.Value) != 2 {
		t.Fatal("incorrect out-of-order window count")
	}
	if (<-output).Type != OutputWatermark {
		t.Fatal("window result followed watermark")
	}
	checkpoint, err := op.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	recovered := build()
	if err := recovered.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	results, err := recovered.OnWatermark(context.Background(), 10)
	if err != nil || len(results) != 0 {
		t.Fatal("restored window fired twice")
	}
	late := 0
	recovered.Late = func(context.Context, Event) error { late++; return nil }
	if err := recovered.FlatMap(context.Background(), Event{EventTime: 2}, func(Event) { t.Fatal("late record emitted") }); err != nil {
		t.Fatal(err)
	}
	if late != 1 {
		t.Fatal("late record was not routed")
	}
}
