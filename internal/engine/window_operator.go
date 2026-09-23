package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/tarungka/wire/internal/observability"
)

// EventTimeWindowOperator connects window state to ordered task processing.
// EncodeResult defines the downstream record format; Late receives records
// beyond allowed lateness. A nil Late callback drops those records.
type EventTimeWindowOperator struct {
	LateOutputTag       string
	processor           *WindowProcessor
	encodeResult        func(WindowResult) Event
	ResultEvents        func(context.Context, WindowResult) ([]Event, error)
	Late                func(context.Context, Event) error
	operatorID, taskID  string
	metrics             *observability.WindowRecorder
	StateBackendFactory func() (StateBackend, func(), error)
	backend             StateBackend
	cleanup             func()
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
	factory := op.StateBackendFactory
	if factory == nil {
		factory = func() (StateBackend, func(), error) {
			dir, err := os.MkdirTemp("", "wire-window-")
			if err != nil {
				return nil, nil, err
			}
			cleanup := func() { _ = os.RemoveAll(dir) }
			backend, err := NewStateBackend(StateBackendConfig{Type: StateBackendPebble, PebbleDataDir: dir})
			if err != nil {
				cleanup()
				return nil, nil, err
			}
			return backend, cleanup, nil
		}
	}
	backend, cleanup, err := factory()
	if err != nil {
		return err
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	if err = op.processor.BindBackend(backend); err != nil {
		_ = backend.Close()
		cleanup()
		return err
	}
	op.backend, op.cleanup = backend, cleanup

	if op.operatorID == "" {
		op.operatorID = op.processor.config.AggregationID
	}
	recorder, err := observability.NewWindowRecorder(op.operatorID, op.taskID, func() int64 { return op.processor.Stats().RetentionBytes })
	if err != nil {
		_ = op.Close()
		return err
	}
	op.metrics = recorder
	return nil
}
func (op *EventTimeWindowOperator) Close() error {
	err := op.metrics.Close()
	if op.backend != nil {
		err = errors.Join(err, op.backend.Close())
		op.backend = nil
	}
	if op.cleanup != nil {
		op.cleanup()
		op.cleanup = nil
	}
	return err
}
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
	if late && op.LateOutputTag != "" {
		event.sideOutput = op.LateOutputTag
		emit(event)
		return nil
	}
	if late && op.Late != nil {
		return op.Late(ctx, event)
	}
	events, err := op.encode(ctx, results)
	if err != nil {
		return err
	}
	for _, event := range events {
		emit(event)
	}
	return nil
}
func (op *EventTimeWindowOperator) OnWatermark(ctx context.Context, timestamp int64) ([]Event, error) {
	results, err := op.processor.AdvanceWatermarkChecked(timestamp)
	if err != nil {
		return nil, err
	}
	return op.encode(ctx, results)
}
func (op *EventTimeWindowOperator) encode(ctx context.Context, results []WindowResult) ([]Event, error) {
	var events []Event
	for _, result := range results {
		var records []Event
		if op.ResultEvents != nil {
			var err error
			records, err = op.ResultEvents(ctx, result)
			if err != nil {
				return nil, err
			}
		} else {
			records = []Event{op.encodeResult(result)}
		}
		for _, event := range records {
			events = append(events, WithWindowResultMetadata(event, result))
		}
	}
	return events, nil
}

// SetLateOutputTag selects a routed stream instead of the optional callback.
func (op *EventTimeWindowOperator) SetLateOutputTag(tag string) { op.LateOutputTag = tag }

// ConfigureWindow applies SDK dimensions before Open without changing the
// factory's aggregation identity, state limits, encoder or backend choice.
func (op *EventTimeWindowOperator) ConfigureWindow(kind string, size, slide, gap, lateness int64) error {
	if op.backend != nil || op.processor.watermark != math.MinInt64 || op.processor.counters() != (WindowStats{}) {
		return fmt.Errorf("window: configuration must precede Open/processing")
	}
	cfg := op.processor.config
	cfg.Kind, cfg.Size, cfg.Slide, cfg.Gap, cfg.AllowedLateness = kind, size, slide, gap, lateness
	processor, err := NewWindowProcessor(cfg, op.processor.aggregator)
	if err != nil {
		return err
	}
	op.processor = processor
	return nil
}

// SetStateBackendFactory applies a per-job backend before Open.
func (op *EventTimeWindowOperator) SetStateBackendFactory(factory func() (StateBackend, func(), error)) {
	op.StateBackendFactory = factory
}
