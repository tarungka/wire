package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

// runWatermarkEmitter periodically generates watermarks from a WatermarkStrategy
// and sends them to outputCh. It runs only for source tasks.
//
// The watermark is maintained as an atomic.Int64 shared with input readers.
// This goroutine CAS-advances the watermark and emits OutputWatermark messages.
func runWatermarkEmitter(
	ctx context.Context,
	strategy WatermarkStrategy,
	watermark *atomic.Int64,
	outputCh chan<- OutputMsg,
	interval time.Duration,
	log zerolog.Logger,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ts := strategy.GenerateWatermark()
			// CAS loop: only advance.
			for {
				cur := watermark.Load()
				if ts <= cur {
					break
				}
				if watermark.CompareAndSwap(cur, ts) {
					break
				}
			}

			current := watermark.Load()
			msg := OutputMsg{
				Type: OutputWatermark,
				Watermark: &protocol.WatermarkMsg{
					Timestamp: current,
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
