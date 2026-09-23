package engine

import (
	"context"

	"github.com/tarungka/wire/internal/protocol"
)

// WatermarkOperator receives an ordered event-time boundary on the same
// goroutine as record processing. Its results traverse subsequent operators
// before the watermark is forwarded downstream.
type WatermarkOperator interface {
	OnWatermark(context.Context, int64) ([]Event, error)
}

func processWatermark(cc *chainContext, timestamp int64) error {
	if cc.watermarkSet && timestamp <= cc.lastWatermark {
		return nil
	}
	cc.lastWatermark, cc.watermarkSet = timestamp, true
	for index, link := range cc.links {
		operator, ok := link.Operator.(WatermarkOperator)
		if !ok {
			continue
		}
		var events []Event
		if err := invokeOperator(func() error { var err error; events, err = operator.OnWatermark(cc.ctx, timestamp); return err }); err != nil {
			return err
		}
		for _, event := range events {
			if err := processEventFrom(cc, event, cc.links[index+1:]); err != nil {
				return err
			}
		}
	}
	return cc.sendOutput(OutputMsg{Type: OutputWatermark, Watermark: &protocol.WatermarkMsg{Timestamp: timestamp}})
}

// inputWatermarkBoundary shares the data queue and checkpoint side buffer.
// Updating the tracker here prevents a read-ahead watermark from skipping data.
type inputWatermarkBoundary struct {
	tracker   *InputWatermarkTracker
	input     int
	timestamp int64
}

// WatermarkEvent constructs an internal queue boundary for in-process routing.
// It must be enqueued after all preceding records from the relevant inputs.
func WatermarkEvent(timestamp int64) Event { return Event{watermark: &timestamp} }
