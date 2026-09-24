package engine

import (
	"context"
	"math"
	"testing"

	"github.com/rs/zerolog"
)

func TestSourceReaderAssignsIngestionTimestamp(t *testing.T) {
	original := Event{Key: []byte("key"), Value: []byte("value"), EventTime: 42}
	source := newSlowMockSource([][]Event{{original}}, 0)
	events := make(chan Event, 2)
	controls := make(chan ControlMsg, 1)
	strategy := &IngestionTimeStrategy{clock: func() int64 { return 123456 }}
	if err := runSourceReader(context.Background(), source, strategy, events, controls, zerolog.Nop()); err != nil {
		t.Fatal(err)
	}
	got := <-events
	if got.EventTime != 123456 || string(got.Key) != "key" || string(got.Value) != "value" {
		t.Fatalf("ingested event: %+v", got)
	}
	if original.EventTime != 42 {
		t.Fatal("source-owned event mutated")
	}
	if terminal := <-events; terminal.watermark == nil || *terminal.watermark != math.MaxInt64 {
		t.Fatal("missing terminal source watermark")
	}
	if (<-controls).Type != CtrlEndOfPartition {
		t.Fatal("missing source completion")
	}
}
