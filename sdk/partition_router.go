package sdk

import (
	"context"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
)

// partitionRouter reads OutputMsg from upstream output channels and routes
// events to downstream input channels based on a routing strategy.
type partitionRouter struct {
	upstreams   []<-chan engine.OutputMsg
	downstreams []chan<- engine.Event
	controlChs  []chan<- engine.ControlMsg
	routeFn     func(event engine.Event, numDown int) int
	keySelector KeySelector
}

// hashRouter returns a routing function that partitions by key hash.
func hashRouter() func(engine.Event, int) int {
	return func(event engine.Event, numDown int) int {
		return int(keygroup.KeyGroup(event.Key, numDown))
	}
}

// forwardRouter returns a routing function for 1:1 forwarding.
func forwardRouter(index int) func(engine.Event, int) int {
	return func(_ engine.Event, _ int) int {
		return index
	}
}

// rebalanceRouter returns a round-robin routing function.
func rebalanceRouter() func(engine.Event, int) int {
	var counter atomic.Uint64
	return func(_ engine.Event, numDown int) int {
		n := counter.Add(1)
		return int((n - 1) % uint64(numDown))
	}
}

// run starts the router. It reads from all upstream channels and routes events
// to downstream channels. When all upstreams are exhausted, it sends an
// EndOfPartition control message to all downstreams and closes them.
func (r *partitionRouter) run(ctx context.Context) error {
	defer func() {
		for _, ch := range r.downstreams {
			close(ch)
		}
	}()
	g, gctx := errgroup.WithContext(ctx)
	for _, upstream := range r.upstreams {
		g.Go(func() error {
			for {
				var msg engine.OutputMsg
				select {
				case <-gctx.Done():
					return gctx.Err()
				case value, ok := <-upstream:
					if !ok {
						return nil
					}
					msg = value
				}
				switch msg.Type {
				case engine.OutputData:
					if r.keySelector != nil {
						key, err := r.keySelector(msg.Event)
						if err != nil {
							return err
						}
						msg.Event.Key = append([]byte(nil), key...)
					}
					target := r.routeFn(msg.Event, len(r.downstreams))
					select {
					case r.downstreams[target] <- msg.Event:
					case <-gctx.Done():
						return gctx.Err()
					}
				case engine.OutputBarrier:
					for _, ch := range r.controlChs {
						select {
						case ch <- engine.ControlMsg{Type: engine.CtrlBarrierReceived, CheckpointID: msg.Barrier.CheckpointID, EpochID: msg.Barrier.EpochID}:
						case <-gctx.Done():
							return gctx.Err()
						}
					}
				}
			}
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for i, ch := range r.controlChs {
		select {
		case ch <- engine.ControlMsg{Type: engine.CtrlEndOfPartition, InputIndex: i}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
