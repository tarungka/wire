package engine

import (
	"context"
	"fmt"
)

// EventTimeWindowOperator connects window state to ordered task processing.
// EncodeResult defines the downstream record format; Late receives records
// beyond allowed lateness. A nil Late callback drops those records.
type EventTimeWindowOperator struct {
	processor    *WindowProcessor
	encodeResult func(WindowResult) Event
	Late         func(context.Context, Event) error
}

func NewEventTimeWindowOperator(config WindowConfig, aggregator WindowAggregator, encodeResult func(WindowResult) Event) (*EventTimeWindowOperator, error) {
	if encodeResult == nil {
		return nil, fmt.Errorf("window result encoder is required")
	}
	processor, err := NewWindowProcessor(config, aggregator)
	if err != nil {
		return nil, err
	}
	return &EventTimeWindowOperator{processor: processor, encodeResult: encodeResult}, nil
}
func (*EventTimeWindowOperator) Open(context.Context) error { return nil }
func (*EventTimeWindowOperator) Close() error               { return nil }
func (op *EventTimeWindowOperator) Checkpoint(id uint64) ([]byte, error) {
	return op.processor.Checkpoint(id)
}
func (op *EventTimeWindowOperator) RestoreCheckpoint(data []byte) error {
	return op.processor.Restore(data)
}
func (op *EventTimeWindowOperator) FlatMap(ctx context.Context, event Event, emit func(Event)) error {
	results, late, err := op.processor.Process(event)
	if err != nil {
		return err
	}
	if late && op.Late != nil {
		return op.Late(ctx, event)
	}
	for _, result := range results {
		emit(op.encodeResult(result))
	}
	return nil
}
func (op *EventTimeWindowOperator) OnWatermark(_ context.Context, timestamp int64) ([]Event, error) {
	results := op.processor.AdvanceWatermark(timestamp)
	events := make([]Event, 0, len(results))
	for _, result := range results {
		events = append(events, op.encodeResult(result))
	}
	return events, nil
}
