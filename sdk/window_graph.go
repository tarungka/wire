package sdk

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
)

// runGraph preserves the graph's main and side-output edges instead of flattening
// topological order into one chain. One router owns each node's input channels;
// all parent streams participate in its minimum-watermark calculation.
func (ex *embeddedExecutor) runGraph(ctx context.Context, sorted []*StreamNode, job string, start time.Time, log zerolog.Logger) (*JobResult, error) {
	type feed struct {
		tag     string
		channel chan engine.OutputMsg
	}
	ios := make(map[int]stageIO)
	incoming := make(map[int][]<-chan engine.OutputMsg)
	routes := make(map[int][]func(engine.Event, int) int)
	outgoing := make(map[int][][]feed)
	parallelism := make(map[int]int)
	idleTimeouts := make(map[int]time.Duration)
	inputTimeouts := make(map[int][]time.Duration)
	for _, node := range sorted {
		idle := time.Duration(0)
		for _, edge := range ex.env.graph.upstream(node.ID) {
			if idleTimeouts[edge.SourceID] > idle {
				idle = idleTimeouts[edge.SourceID]
			}
		}
		if idle == 0 {
			idle = engine.DefaultIdleTimeout
		}
		if node.Type == NodeSource && node.Watermark != nil && node.Watermark.IdleTimeout > 0 {
			idle = node.Watermark.IdleTimeout
		}
		idleTimeouts[node.ID] = idle
		p := node.Parallelism
		// A concrete connector is one instance. Parallel connector instances
		// are constructed by factories, never by reopening a shared object.
		if p == 0 && (node.Source != nil || node.Sink != nil) {
			p = 1
		}
		if p > 1 && (node.Source != nil || node.Sink != nil) {
			return nil, fmt.Errorf("sdk: parallel connectors require an instance factory")
		}
		if p <= 0 {
			p = ex.env.parallelism
		}
		if p <= 0 {
			p = 1
		}
		parallelism[node.ID] = p
		channels := stageIO{inputChs: make([]chan engine.Event, p), controlChs: make([]chan engine.ControlMsg, p), outputChs: make([]chan engine.OutputMsg, p)}
		for i := 0; i < p; i++ {
			channels.inputChs[i] = make(chan engine.Event, engine.DefaultInputBufferSize)
			channels.controlChs[i] = make(chan engine.ControlMsg, 8)
			channels.outputChs[i] = make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)
		}
		ios[node.ID] = channels
		outgoing[node.ID] = make([][]feed, p)
	}
	for _, edge := range ex.env.graph.edges {
		source, target := ex.env.graph.nodes[edge.SourceID], ex.env.graph.nodes[edge.TargetID]
		shuffle := edge.Shuffle
		if target.Type == NodeKeyBy {
			shuffle = ShuffleRebalance
		}
		if source.Type == NodeKeyBy {
			shuffle = ShuffleHash
		}
		// Embedded Forward inherits the graph's instance counts; changing explicit
		// counts requires a redistribution, as with a new keyed stage.
		if shuffle == ShuffleForward && parallelism[source.ID] != parallelism[target.ID] {
			shuffle = ShuffleRebalance
		}
		if shuffle != ShuffleForward && shuffle != ShuffleHash && shuffle != ShuffleRebalance && shuffle != ShuffleBroadcast {
			return nil, fmt.Errorf("sdk: unsupported graph edge shuffle")
		}
		if edge.SideOutput != "" && !source.hasSideOutput(edge.SideOutput) {
			return nil, fmt.Errorf("sdk: undeclared side output")
		}
		roundRobin := rebalanceRouter()
		for i := 0; i < parallelism[source.ID]; i++ {
			ch := make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)
			outgoing[source.ID][i] = append(outgoing[source.ID][i], feed{tag: edge.SideOutput, channel: ch})
			incoming[target.ID] = append(incoming[target.ID], ch)
			inputTimeouts[target.ID] = append(inputTimeouts[target.ID], idleTimeouts[source.ID])
			route := roundRobin
			switch shuffle {
			case ShuffleBroadcast:
				route = func(engine.Event, int) int { return -1 }
			case ShuffleHash:
				route = hashRouter()
			case ShuffleForward:
				route = forwardRouter(i)
			}
			routes[target.ID] = append(routes[target.ID], route)
		}
	}
	for _, node := range sorted {
		if node.Type != NodeSource && len(incoming[node.ID]) == 0 {
			return nil, fmt.Errorf("sdk: graph node has no input")
		}
		if node.Type == NodeSource && len(incoming[node.ID]) > 0 {
			return nil, fmt.Errorf("sdk: source cannot accept graph inputs")
		}
	}
	instances := make(map[int][]StreamNode)
	for _, node := range sorted {
		for i := 0; i < parallelism[node.ID]; i++ {
			instance, err := instantiateNode(node, i, parallelism[node.ID])
			if err != nil {
				return nil, err
			}
			instances[node.ID] = append(instances[node.ID], instance)
		}
	}
	group, gctx := errgroup.WithContext(ctx)
	for _, node := range sorted {
		channels := ios[node.ID]
		if node.Type != NodeSource {
			router := &partitionRouter{upstreams: incoming[node.ID], inputRoutes: routes[node.ID], inputIdleTimeouts: inputTimeouts[node.ID]}
			for i := range channels.inputChs {
				router.downstreams = append(router.downstreams, channels.inputChs[i])
				router.controlChs = append(router.controlChs, channels.controlChs[i])
			}
			group.Go(func() error { return router.run(gctx) })
		}
		for i := 0; i < parallelism[node.ID]; i++ {
			execution := instances[node.ID][i]
			if node.Type == NodeKeyBy {
				execution.Type = NodeMap
				execution.MapFn = func(event Event) (Event, error) {
					key, err := node.KeyByFn(event)
					if err != nil {
						return Event{}, err
					}
					event.Key = bytes.Clone(key)
					return event, nil
				}
			}
			group.Go(func() error {
				return ex.runStageInstance(gctx, []*StreamNode{&execution}, i, node.Type == NodeSource, channels, log)
			})
			destinations := outgoing[node.ID][i]
			group.Go(func() error {
				defer func() {
					for _, destination := range destinations {
						close(destination.channel)
					}
				}()
				for {
					var message engine.OutputMsg
					select {
					case <-gctx.Done():
						return gctx.Err()
					case msg, ok := <-channels.outputChs[i]:
						if !ok {
							return nil
						}
						message = msg
					}
					for _, destination := range destinations {
						if message.Type == engine.OutputData && message.SideOutput != destination.tag {
							continue
						}
						routed := message
						routed.SideOutput = ""
						if message.Type == engine.OutputData && len(destinations) > 1 {
							routed.Event.Key = bytes.Clone(message.Event.Key)
							routed.Event.Value = bytes.Clone(message.Event.Value)
							if message.Event.Headers != nil {
								routed.Event.Headers = make(map[string][]byte, len(message.Event.Headers))
								for key, value := range message.Event.Headers {
									routed.Event.Headers[key] = bytes.Clone(value)
								}
							}
						}
						select {
						case destination.channel <- routed:
						case <-gctx.Done():
							return gctx.Err()
						}
					}
				}
			})
		}
	}
	err := group.Wait()
	return &JobResult{JobID: job, Err: err, Metrics: JobMetrics{Duration: time.Since(start)}}, err
}
