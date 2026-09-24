package engine

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestSourceTerminalWatermarkStopsPeriodicEmission(t *testing.T) {
	queue := &sourceWatermarkQueue{}
	events := make(chan Event, 3)
	strategy := NewMonotonicTimestampsStrategy()
	if err := dispatchSourceBatch(t.Context(), []Event{{EventTime: 7}}, strategy, events, queue); err != nil {
		t.Fatal(err)
	}
	if err := queue.finish(t.Context(), events); err != nil {
		t.Fatal(err)
	}
	last := int64(math.MinInt64)
	if err := queue.emit(t.Context(), strategy, events, &last); err != nil {
		t.Fatal(err)
	}
	if err := queue.finish(t.Context(), events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatal("boundary emitted after terminal watermark")
	}
	if record := <-events; record.EventTime != 7 || record.watermark != nil {
		t.Fatal("terminal watermark overtook data")
	}
	if terminal := <-events; terminal.watermark == nil || *terminal.watermark != math.MaxInt64 {
		t.Fatal("missing terminal watermark")
	}
}

type failingWatermarkStrategy struct{}

func (failingWatermarkStrategy) GenerateWatermark() int64 { panic("watermark failed") }
func (failingWatermarkStrategy) ObserveEventTime(int64)   {}

func TestSourceWatermarkFailureCancelsIdleReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := newSlowMockSource(nil, time.Hour)
	err := RunSourceReaderWithWatermarks(ctx, source, failingWatermarkStrategy{}, make(chan Event), make(chan ControlMsg), time.Millisecond, testLogger())
	if !errors.Is(err, ErrOperatorPanic) {
		t.Fatalf("expected emitter panic, got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("reader stopped only when parent timed out")
	}
}

func TestSourceWatermarkCannotOvertakeBlockedBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queue := &sourceWatermarkQueue{}
	strategy := NewMonotonicTimestampsStrategy()
	events := make(chan Event)
	dispatched := make(chan error, 1)
	go func() {
		dispatched <- dispatchSourceBatch(ctx, []Event{{EventTime: 100}, {EventTime: 200}}, strategy, events, queue)
	}()
	first := <-events
	if first.EventTime != 100 {
		t.Fatal("first record missing")
	}
	emitted := make(chan error, 1)
	go func() { var last int64; emitted <- queue.emit(ctx, strategy, events, &last) }()
	second := <-events
	if second.watermark != nil || second.EventTime != 200 {
		t.Fatal("watermark overtook blocked record")
	}
	boundary := <-events
	if boundary.watermark == nil || *boundary.watermark != 200 {
		t.Fatal("watermark did not include completed batch")
	}
	if err := <-dispatched; err != nil {
		t.Fatal(err)
	}
	if err := <-emitted; err != nil {
		t.Fatal(err)
	}
}
