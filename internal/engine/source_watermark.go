package engine

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// sourceWatermarkQueue serializes record dispatch and periodic boundaries.
// ReadBatch itself runs outside this lock so an idle source cannot stop ticks.
type sourceWatermarkQueue struct {
	mu       sync.Mutex
	finished bool
}

func (q *sourceWatermarkQueue) emit(ctx context.Context, strategy WatermarkStrategy, events chan<- Event, timestamp *int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	next := strategy.GenerateWatermark()
	if next <= *timestamp {
		return nil
	}
	select {
	case events <- Event{watermark: &next}:
		*timestamp = next
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *sourceWatermarkQueue) run(ctx context.Context, strategy WatermarkStrategy, events chan<- Event, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := int64(math.MinInt64)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := q.emit(ctx, strategy, events, &last); err != nil {
				return err
			}
		}
	}
}

func dispatchSourceBatch(ctx context.Context, batch []Event, strategy WatermarkStrategy, events chan<- Event, queue *sourceWatermarkQueue) error {
	if queue != nil {
		queue.mu.Lock()
		defer queue.mu.Unlock()
	}
	for _, event := range batch {
		if ingestion, ok := strategy.(*IngestionTimeStrategy); ok {
			event.EventTime = ingestion.clock()
		}
		select {
		case events <- event:
			if strategy != nil {
				strategy.ObserveEventTime(event.EventTime)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// RunSourceReaderWithWatermarks runs SDK source intake and periodic ordered
// boundaries together. It returns only after both producers have stopped.
func RunSourceReaderWithWatermarks(ctx context.Context, source SourceOperator, strategy WatermarkStrategy, events chan<- Event, controls chan<- ControlMsg, interval time.Duration, log zerolog.Logger) error {
	if strategy == nil {
		return runSourceReader(ctx, source, nil, events, controls, log)
	}
	if interval <= 0 {
		interval = DefaultWatermarkInterval
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	queue := &sourceWatermarkQueue{}
	done := make(chan error, 1)
	go func() {
		emitterErr := invokeOperator(func() error { return queue.run(runCtx, strategy, events, interval) })
		// A failed emitter must also stop intake, including an idle ReadBatch.
		cancel()
		done <- emitterErr
	}()
	boundary := &sourceCheckpointInput{watermarks: queue}
	err := invokeOperator(func() error {
		return runSourceReaderWithContexts(runCtx, runCtx, source, strategy, events, controls, log, boundary)
	})
	cancel()
	emitterErr := <-done
	if emitterErr != nil && !errors.Is(emitterErr, context.Canceled) {
		return emitterErr
	}
	return err
}

// finish queues the terminal watermark after all source records, before the
// final checkpoint snapshots timer/window output. No periodic boundary may
// follow it, even if the emitter is already waiting for this lock.
func (q *sourceWatermarkQueue) finish(ctx context.Context, events chan<- Event) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.finished {
		return nil
	}
	select {
	case events <- WatermarkEvent(math.MaxInt64):
		q.finished = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
