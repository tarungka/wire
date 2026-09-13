package sdk

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
)

// partitionRouter reads OutputMsg from upstream output channels and routes
// events to downstream input channels based on a routing strategy.
type partitionRouter struct {
	upstreams         []<-chan engine.OutputMsg
	downstreams       []chan<- engine.Event
	controlChs        []chan<- engine.ControlMsg
	routeFn           func(event engine.Event, numDown int) int
	keySelector       KeySelector
	idleTimeout       time.Duration
	watermarkInterval time.Duration
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
	tracker := engine.NewInputWatermarkTracker(len(r.upstreams))
	var watermarkMu sync.Mutex
	lastWatermark := int64(math.MinInt64)
	idleTimeout := r.idleTimeout
	if idleTimeout == 0 {
		idleTimeout = engine.DefaultIdleTimeout
	}
	interval := r.watermarkInterval
	if interval <= 0 {
		interval = engine.DefaultWatermarkInterval
	}
	emitMinimum := func() error {
		minimum, idle := tracker.MinWatermark(idleTimeout)
		if idle || minimum <= lastWatermark {
			return nil
		}
		for _, channel := range r.downstreams {
			select {
			case channel <- engine.WatermarkEvent(minimum):
			case <-gctx.Done():
				return gctx.Err()
			}
		}
		lastWatermark = minimum
		return nil
	}
	var producers sync.WaitGroup
	producers.Add(len(r.upstreams))
	producersDone := make(chan struct{})
	for inputIndex, upstream := range r.upstreams {
		g.Go(func() error {
			defer producers.Done()
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

					if err := func() error {
						// Keep idle exclusion and watermark fan-out behind any
						// record currently blocked on downstream capacity.
						watermarkMu.Lock()
						defer watermarkMu.Unlock()
						tracker.RecordActivity(inputIndex)
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
							tracker.RecordActivity(inputIndex)
							return nil
						case <-gctx.Done():
							return gctx.Err()
						}
					}(); err != nil {
						return err
					}
				case engine.OutputWatermark:
					if msg.Watermark == nil {
						continue
					}
					// Serialize the minimum and its fan-out so concurrent producers cannot
					// send different watermark generations in opposite orders.
					if err := func() error {
						watermarkMu.Lock()
						defer watermarkMu.Unlock()
						tracker.AdvanceWatermark(inputIndex, msg.Watermark.Timestamp)
						return emitMinimum()
					}(); err != nil {
						return err
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
	go func() { producers.Wait(); close(producersDone) }()
	g.Go(func() error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case <-producersDone:
				return nil
			case <-ticker.C:
				watermarkMu.Lock()
				err := emitMinimum()
				watermarkMu.Unlock()
				if err != nil {
					return err
				}
			}
		}
	})
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
