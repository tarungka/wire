package engine

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

// runWatermarkEmitter periodically generates watermarks from a WatermarkStrategy
// and sends them to outputCh. It runs only for source tasks.
//
// A local lastEmitted variable ensures monotonic emission (matching the
// propagator pattern). Only watermarks that advance beyond lastEmitted are sent.
func runWatermarkEmitter(
	ctx context.Context,
	strategy WatermarkStrategy,
	outputCh chan<- OutputMsg,
	interval time.Duration,
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
			ts := strategy.GenerateWatermark()
			if ts <= lastEmitted {
				continue
			}
			lastEmitted = ts

			msg := OutputMsg{
				Type: OutputWatermark,
				Watermark: &protocol.WatermarkMsg{
					Timestamp: ts,
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
