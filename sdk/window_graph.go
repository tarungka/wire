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

// runWindowGraph preserves the graph's main/late edges instead of flattening
// topological order into one chain. One router owns each node's input channels;
// all parent streams participate in its minimum-watermark calculation.
func (ex *embeddedExecutor) runWindowGraph(ctx context.Context, sorted []*StreamNode, job string, start time.Time, log zerolog.Logger) (*JobResult, error) {
	type feed struct {
		tag     string
		channel chan engine.OutputMsg
	}
	ios := make(map[int]stageIO)
	incoming := make(map[int][]<-chan engine.OutputMsg)
	routes := make(map[int][]func(engine.Event, int) int)
	outgoing := make(map[int][][]feed)
	parallelism := make(map[int]int)
	for _, node := range sorted {
		p := node.Parallelism
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
		if shuffle != ShuffleForward && shuffle != ShuffleHash && shuffle != ShuffleRebalance {
			return nil, fmt.Errorf("sdk: unsupported window edge shuffle")
		}
		if edge.SideOutput != "" && source.LateOutputTag != edge.SideOutput {
			return nil, fmt.Errorf("sdk: undeclared late output")
		}
		roundRobin := rebalanceRouter()
		for i := 0; i < parallelism[source.ID]; i++ {
			ch := make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)
			outgoing[source.ID][i] = append(outgoing[source.ID][i], feed{tag: edge.SideOutput, channel: ch})
			incoming[target.ID] = append(incoming[target.ID], ch)
			route := roundRobin
			switch shuffle {
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
			return nil, fmt.Errorf("sdk: window graph node has no input")
		}
		if node.Type == NodeSource && len(incoming[node.ID]) > 0 {
			return nil, fmt.Errorf("sdk: source cannot accept graph inputs")
		}
	}
	group, gctx := errgroup.WithContext(ctx)
	for _, node := range sorted {
		channels := ios[node.ID]
		if node.Type != NodeSource {
			router := &partitionRouter{upstreams: incoming[node.ID], inputRoutes: routes[node.ID]}
			for i := range channels.inputChs {
				router.downstreams = append(router.downstreams, channels.inputChs[i])
				router.controlChs = append(router.controlChs, channels.controlChs[i])
			}
			group.Go(func() error { return router.run(gctx) })
		}
		execution := *node
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
		for i := 0; i < parallelism[node.ID]; i++ {
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
