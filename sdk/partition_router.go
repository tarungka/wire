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
	inputRoutes       []func(engine.Event, int) int
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
	downstreamMu := make([]sync.Mutex, len(r.downstreams))
	lastWatermark := int64(math.MinInt64)
	idleTimeout := r.idleTimeout
	if idleTimeout == 0 {
		idleTimeout = engine.DefaultIdleTimeout
	}
	interval := r.watermarkInterval
	if interval <= 0 {
		interval = engine.DefaultWatermarkInterval
	}
	// Publishing a boundary never waits for downstream capacity. Each
	// destination has one delivery goroutine and one coalesced notification.
	watermarkWake := make([]chan struct{}, len(r.downstreams))
	for target := range watermarkWake {
		watermarkWake[target] = make(chan struct{}, 1)
	}
	publishMinimum := func() {
		watermarkMu.Lock()
		defer watermarkMu.Unlock()
		minimum, idle := tracker.MinWatermark(idleTimeout)
		if idle || minimum <= lastWatermark {
			return
		}
		lastWatermark = minimum
		for _, wake := range watermarkWake {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}
	watermarksDone := make(chan struct{})
	for target, channel := range r.downstreams {
		g.Go(func() error {
			lastSent := int64(math.MinInt64)
			deliver := func() error {
				watermarkMu.Lock()
				minimum := lastWatermark
				watermarkMu.Unlock()
				if minimum <= lastSent {
					return nil
				}
				downstreamMu[target].Lock()
				defer downstreamMu[target].Unlock()
				select {
				case channel <- engine.WatermarkEvent(minimum):
					lastSent = minimum
					return nil
				case <-gctx.Done():
					return gctx.Err()
				}
			}
			for {
				select {
				case <-gctx.Done():
					return gctx.Err()
				case <-watermarksDone:
					// All publishers have stopped. Flush the newest boundary
					// before run closes downstreams and emits end-of-partition.
					return deliver()
				case <-watermarkWake[target]:
					if err := deliver(); err != nil {
						return err
					}
				}
			}
		})
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
						// Pending records prevent idle exclusion during backpressure.
						// Only this destination is serialized with watermark sends.
						tracker.RecordQueued(inputIndex)
						defer tracker.RecordProcessed(inputIndex)
						if r.keySelector != nil {
							key, err := r.keySelector(msg.Event)
							if err != nil {
								return err
							}
							msg.Event.Key = append([]byte(nil), key...)
						}
						route := r.routeFn
						if len(r.inputRoutes) > 0 {
							route = r.inputRoutes[inputIndex]
						}
						target := route(msg.Event, len(r.downstreams))
						downstreamMu[target].Lock()
						defer downstreamMu[target].Unlock()
						select {
						case r.downstreams[target] <- msg.Event:
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
					tracker.AdvanceWatermark(inputIndex, msg.Watermark.Timestamp)
					publishMinimum()
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
		defer close(watermarksDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case <-producersDone:
				return nil
			case <-ticker.C:
				publishMinimum()
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
