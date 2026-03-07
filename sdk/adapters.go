package sdk

import (
	"context"

	"github.com/tarungka/wire/internal/engine"
)

// mapAdapter wraps a MapFunc to implement engine.MapOperator.
type mapAdapter struct {
	fn MapFunc
}

func (a *mapAdapter) Open(_ context.Context) error        { return nil }
func (a *mapAdapter) Close() error                        { return nil }
func (a *mapAdapter) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (a *mapAdapter) Map(_ context.Context, event Event) (Event, error) {
	return a.fn(event)
}

// flatMapAdapter wraps a FlatMapFunc to implement engine.FlatMapOperator.
// The engine's FlatMap uses an emit callback; the SDK's FlatMapFunc returns a slice.
// The adapter bridges these by calling emit per-event from the returned slice.
type flatMapAdapter struct {
	fn FlatMapFunc
}

func (a *flatMapAdapter) Open(_ context.Context) error        { return nil }
func (a *flatMapAdapter) Close() error                        { return nil }
func (a *flatMapAdapter) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (a *flatMapAdapter) FlatMap(_ context.Context, event Event, emit func(Event)) error {
	results, err := a.fn(event)
	if err != nil {
		return err
	}
	for _, e := range results {
		emit(e)
	}
	return nil
}

// filterAdapter wraps a FilterFunc to implement engine.MapOperator.
// When the predicate returns false, it returns a zero Event (which the engine
// recognizes as filtered via isZeroEvent).
type filterAdapter struct {
	fn FilterFunc
}

func (a *filterAdapter) Open(_ context.Context) error        { return nil }
func (a *filterAdapter) Close() error                        { return nil }
func (a *filterAdapter) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (a *filterAdapter) Map(_ context.Context, event Event) (Event, error) {
	keep, err := a.fn(event)
	if err != nil {
		return Event{}, err
	}
	if !keep {
		return Event{}, nil // Zero event = filtered.
	}
	return event, nil
}

// sourceAdapter wraps an sdk.Source to implement engine.SourceOperator.
type sourceAdapter struct {
	source Source
}

func (a *sourceAdapter) Open(ctx context.Context) error      { return a.source.Open(ctx) }
func (a *sourceAdapter) Close() error                        { return a.source.Close() }
func (a *sourceAdapter) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (a *sourceAdapter) ReadBatch(ctx context.Context) ([]Event, error) {
	return a.source.ReadBatch(ctx)
}
func (a *sourceAdapter) GenerateWatermark() int64 { return a.source.GenerateWatermark() }

// sinkAdapter wraps an sdk.Sink to implement engine.SinkOperator.
type sinkAdapter struct {
	sink Sink
}

func (a *sinkAdapter) Open(ctx context.Context) error               { return a.sink.Open(ctx) }
func (a *sinkAdapter) Close() error                                 { return a.sink.Close() }
func (a *sinkAdapter) Checkpoint(_ uint64) ([]byte, error)          { return nil, nil }
func (a *sinkAdapter) Write(ctx context.Context, event Event) error { return a.sink.Write(ctx, event) }

// Compile-time interface checks.
var (
	_ engine.MapOperator     = (*mapAdapter)(nil)
	_ engine.FlatMapOperator = (*flatMapAdapter)(nil)
	_ engine.MapOperator     = (*filterAdapter)(nil)
	_ engine.SourceOperator  = (*sourceAdapter)(nil)
	_ engine.SinkOperator    = (*sinkAdapter)(nil)
)
