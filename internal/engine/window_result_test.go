package engine

import (
	"context"
	"encoding/binary"
	"testing"
)

func TestWindowResultMetadataPreservesUpdatesAcrossTransport(t *testing.T) {
	op, err := NewEventTimeWindowOperator(WindowConfig{Kind: "tumbling", Size: 10, AllowedLateness: 5, AggregationID: "count"}, windowCount{}, func(r WindowResult) Event { return Event{Key: r.Key, Value: r.Value, EventTime: r.WindowEnd} })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = op.FlatMap(ctx, Event{Key: []byte("k"), EventTime: 1}, func(Event) { t.Fatal("early result") }); err != nil {
		t.Fatal(err)
	}
	events, err := op.OnWatermark(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	check := func(event Event, updated bool, count uint64) {
		t.Helper()
		result, ok, err := DecodeWindowResult(EventFromProto(event.ToProto()))
		if err != nil || !ok || result.WindowStart != 0 || result.WindowEnd != 10 || result.IsUpdate != updated || binary.BigEndian.Uint64(result.Value) != count {
			t.Fatalf("result=%+v ok=%t err=%v", result, ok, err)
		}
	}
	check(events[0], false, 1)
	if err = op.FlatMap(ctx, Event{Key: []byte("k"), EventTime: 2}, func(event Event) { check(event, true, 2) }); err != nil {
		t.Fatal(err)
	}
	if got := op.processor.Stats().RetentionBytes; got != 9 {
		t.Fatalf("retention bytes=%d", got)
	}
	_, _ = op.OnWatermark(ctx, 15)
	if got := op.processor.Stats().RetentionBytes; got != 0 {
		t.Fatalf("purged retention bytes=%d", got)
	}
}

func TestWindowResultRejectsMalformedMetadata(t *testing.T) {
	if _, ok, err := DecodeWindowResult(Event{}); ok || err != nil {
		t.Fatal("ordinary event misclassified")
	}
	for _, headers := range []map[string][]byte{
		{WindowStartHeader: []byte("0")},
		{WindowStartHeader: []byte("bad"), WindowEndHeader: []byte("10"), WindowUpdateHeader: []byte("true")},
		{WindowStartHeader: []byte("0"), WindowEndHeader: []byte("0"), WindowUpdateHeader: []byte("true")},
		{WindowStartHeader: []byte("0"), WindowEndHeader: []byte("10"), WindowUpdateHeader: []byte("bad")},
	} {
		if _, ok, err := DecodeWindowResult(Event{Headers: headers}); !ok || err == nil {
			t.Fatalf("accepted %+v", headers)
		}
	}
}
