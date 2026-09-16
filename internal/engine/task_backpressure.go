package engine

import (
	"time"
)

type taskBackpressureKey struct{}

// sendOutput measures only sends that cannot proceed immediately. It includes
// a blocked send ended by cancellation; ready sends do not read the clock.
func (cc *chainContext) sendOutput(message OutputMsg) error {
	select {
	case <-cc.ctx.Done():
		return cc.ctx.Err()
	case cc.outputCh <- message:
		if message.Type == OutputData {
			recordTaskOutput(cc.ctx, message.Event)
		}
		return nil
	default:
	}
	start := time.Now()
	defer func() {
		recordTaskBackpressure(cc.ctx, time.Since(start))
		if record, ok := cc.ctx.Value(taskBackpressureKey{}).(func(time.Duration)); ok {
			record(time.Since(start))
		}
	}()
	select {
	case <-cc.ctx.Done():
		return cc.ctx.Err()
	case cc.outputCh <- message:
		if message.Type == OutputData {
			recordTaskOutput(cc.ctx, message.Event)
		}
		return nil
	}
}
