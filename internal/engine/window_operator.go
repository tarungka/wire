package engine

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/internal/observability"
)

// EventTimeWindowOperator connects window state to ordered task processing.
// EncodeResult defines the downstream record format; Late receives records
// beyond allowed lateness. A nil Late callback drops those records.
type EventTimeWindowOperator struct {
	processor          *WindowProcessor
	encodeResult       func(WindowResult) Event
	Late               func(context.Context, Event) error
	operatorID, taskID string
	metrics            *observability.WindowRecorder
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

// SetMetricIdentity is called before Open by the owning task runtime.
func (op *EventTimeWindowOperator) SetMetricIdentity(operatorID, taskID string) {
	op.operatorID, op.taskID = operatorID, taskID
}
func (op *EventTimeWindowOperator) Open(context.Context) error {
	if op.operatorID == "" {
		op.operatorID = op.processor.config.AggregationID
	}
	recorder, err := observability.NewWindowRecorder(op.operatorID, op.taskID, func() int64 { return op.processor.Stats().RetentionBytes })
	if err != nil {
		return err
	}
	op.metrics = recorder
	return nil
}
func (op *EventTimeWindowOperator) Close() error { return op.metrics.Close() }
func (op *EventTimeWindowOperator) Checkpoint(id uint64) ([]byte, error) {
	return op.processor.Checkpoint(id)
}
func (op *EventTimeWindowOperator) RestoreCheckpoint(data []byte) error {
	return op.processor.Restore(data)
}
func (op *EventTimeWindowOperator) FlatMap(ctx context.Context, event Event, emit func(Event)) error {
	before := op.processor.counters()
	results, late, err := op.processor.Process(event)
	if err != nil {
		return err
	}
	after := op.processor.counters()
	op.metrics.Record(ctx, after.Late-before.Late, after.Allowed-before.Allowed, after.Dropped-before.Dropped)
	if late && op.Late != nil {
		return op.Late(ctx, event)
	}
	for _, result := range results {
		emit(WithWindowResultMetadata(op.encodeResult(result), result))
	}
	return nil
}
func (op *EventTimeWindowOperator) OnWatermark(_ context.Context, timestamp int64) ([]Event, error) {
	results := op.processor.AdvanceWatermark(timestamp)
	events := make([]Event, 0, len(results))
	for _, result := range results {
		events = append(events, WithWindowResultMetadata(op.encodeResult(result), result))
	}
	return events, nil
}
