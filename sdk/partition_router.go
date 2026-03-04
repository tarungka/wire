package sdk

import (
	"sync"
	"sync/atomic"

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
func (r *partitionRouter) run() {
	numDown := len(r.downstreams)

	// Track how many upstreams have sent EoP.
	var eopCount atomic.Int32

	var wg sync.WaitGroup
	for i, upstream := range r.upstreams {
		wg.Add(1)
		go func(idx int, ch <-chan engine.OutputMsg) {
			defer wg.Done()
			for msg := range ch {
				switch msg.Type {
				case engine.OutputData:
					target := r.routeFn(msg.Event, numDown)
					r.downstreams[target] <- msg.Event

				case engine.OutputEnd:
					eopCount.Add(1)
					// Don't forward yet — wait until all upstreams finish.

				case engine.OutputBarrier:
					// Forward barrier to all downstreams via control channels.
					for _, ctrlCh := range r.controlChs {
						ctrlCh <- engine.ControlMsg{
							Type:         engine.CtrlBarrierReceived,
							CheckpointID: msg.Barrier.CheckpointID,
							EpochID:      msg.Barrier.EpochID,
						}
					}

				case engine.OutputWatermark:
					// Watermarks are not forwarded through the router in embedded mode.
				}
			}
		}(i, upstream)
	}

	// Wait for all upstreams to finish.
	wg.Wait()

	// Send EoP to all downstream control channels.
	for i, ctrlCh := range r.controlChs {
		ctrlCh <- engine.ControlMsg{
			Type:       engine.CtrlEndOfPartition,
			InputIndex: i,
		}
	}

	// Close downstream event channels.
	for _, ch := range r.downstreams {
		close(ch)
	}
}
