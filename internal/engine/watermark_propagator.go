package engine

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

// runWatermarkPropagator periodically computes the minimum watermark across
// all inputs and emits it downstream. It runs only for non-source tasks
// (multi-input operators).
//
// The propagator mirrors the runWatermarkEmitter pattern but computes Min()
// across per-input watermarks instead of reading from a source strategy.
func runWatermarkPropagator(
	ctx context.Context,
	tracker *InputWatermarkTracker,
	outputCh chan<- OutputMsg,
	interval time.Duration,
	idleTimeout time.Duration,
	log zerolog.Logger,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastEmitted int64

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			minWM, allIdle := tracker.MinWatermark(idleTimeout)
			if allIdle {
				continue // All inputs idle, skip emission.
			}
			if minWM <= lastEmitted {
				continue // No advance, skip emission.
			}

			lastEmitted = minWM
			msg := OutputMsg{
				Type: OutputWatermark,
				Watermark: &protocol.WatermarkMsg{
					Timestamp: minWM,
				},
			}
			select {
			case outputCh <- msg:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
