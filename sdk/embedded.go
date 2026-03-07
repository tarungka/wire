package sdk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/logger"
)

// embeddedExecutor runs pipelines in-process.
type embeddedExecutor struct {
	env *StreamExecutionEnvironment
}

// stageIO holds the channels for a single pipeline stage's parallel instances.
type stageIO struct {
	inputChs   []chan engine.Event
	controlChs []chan engine.ControlMsg
	outputChs  []chan engine.OutputMsg
}

func (ex *embeddedExecutor) run(ctx context.Context, jobName string) (*JobResult, error) {
	start := time.Now()
	log := logger.GetLogger("embedded")

	graph := ex.env.graph
	parallelism := ex.env.parallelism
	if parallelism <= 0 {
		parallelism = 1
	}

	// Topo-sort and check for shuffle boundaries.
	sorted := graph.topoSort()
	hasShuffleBoundary := false
	for _, edge := range graph.edges {
		if edge.Shuffle == ShuffleHash || edge.Shuffle == ShuffleRebalance {
			hasShuffleBoundary = true
			break
		}
	}

	if !hasShuffleBoundary {
		return ex.runLinear(ctx, sorted, parallelism, jobName, start, log)
	}
	return ex.runWithShuffle(ctx, sorted, parallelism, jobName, start, log)
}

// runLinear fuses the entire pipeline into a single operator chain per parallel
// instance. Source→Map→Filter→Sink all become one chain. For parallelism=N,
// create N instances each with their own source.
func (ex *embeddedExecutor) runLinear(
	ctx context.Context,
	sorted []*StreamNode,
	parallelism int,
	jobName string,
	start time.Time,
	log zerolog.Logger,
) (*JobResult, error) {
	g, gctx := errgroup.WithContext(ctx)

	for i := 0; i < parallelism; i++ {
		idx := i
		g.Go(func() error {
			return ex.runLinearInstance(gctx, sorted, idx, log)
		})
	}

	err := g.Wait()
	if err == context.Canceled {
		err = nil
	}

	return &JobResult{
		JobID: jobName,
		Err:   err,
		Metrics: JobMetrics{
			Duration: time.Since(start),
		},
	}, err
}

// runLinearInstance runs a single parallel instance of the full pipeline.
func (ex *embeddedExecutor) runLinearInstance(
	ctx context.Context,
	sorted []*StreamNode,
	instanceIdx int,
	log zerolog.Logger,
) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Build the operator chain from the sorted nodes.
	var operators []engine.Operator
	var sourceOp engine.SourceOperator

	for _, node := range sorted {
		switch node.Type {
		case NodeSource:
			sa := &sourceAdapter{source: node.Source}
			sourceOp = sa
		case NodeMap:
			operators = append(operators, &mapAdapter{fn: node.MapFn})
		case NodeFlatMap:
			operators = append(operators, &flatMapAdapter{fn: node.FlatMapFn})
		case NodeFilter:
			operators = append(operators, &filterAdapter{fn: node.FilterFn})
		case NodeSink:
			operators = append(operators, &sinkAdapter{sink: node.Sink})
		case NodeKeyBy, NodeWindow, NodeReduce, NodeProcess:
			// These shouldn't appear in a linear pipeline.
			return fmt.Errorf("sdk: unexpected node type %d in linear pipeline", node.Type)
		}
	}

	if sourceOp == nil {
		return fmt.Errorf("sdk: no source in pipeline")
	}

	// Create channels.
	eventCh := make(chan engine.Event, engine.DefaultInputBufferSize)
	controlCh := make(chan engine.ControlMsg, 8)
	outputCh := make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)

	aligner := engine.NewBarrierAligner(1, engine.DefaultAlignmentBufferSize)
	metrics := engine.NoopCheckpointMetrics()
	errMetrics := engine.NoopErrorMetrics()
	chainLog := log.With().Int("instance", instanceIdx).Logger()

	ig, igctx := errgroup.WithContext(runCtx)

	// Source reader goroutine.
	ig.Go(func() error {
		return engine.RunSourceReader(igctx, sourceOp, nil, eventCh, controlCh, chainLog)
	})

	// Operator chain goroutine.
	var producerWg sync.WaitGroup
	producerWg.Add(1)
	ig.Go(func() error {
		defer producerWg.Done()
		defer runCancel()
		return engine.RunOperatorChain(igctx, operators, eventCh, controlCh, outputCh, aligner, 1, metrics, chainLog, nil, nil, nil, nil, errMetrics)
	})

	// Close outputCh when operator chain finishes.
	go func() {
		producerWg.Wait()
		close(outputCh)
	}()

	// Drain output channel (linear pipelines have no downstream).
	ig.Go(func() error {
		for range outputCh {
			// Discard — the sink is in the chain.
		}
		return nil
	})

	err := ig.Wait()
	if err == context.Canceled {
		return nil
	}
	return err
}

// runWithShuffle splits the pipeline into stages at shuffle boundaries and
// connects them via partition routers.
func (ex *embeddedExecutor) runWithShuffle(
	ctx context.Context,
	sorted []*StreamNode,
	parallelism int,
	jobName string,
	start time.Time,
	log zerolog.Logger,
) (*JobResult, error) {
	graph := ex.env.graph

	// Split into stages at shuffle boundaries.
	stages := ex.splitStages(sorted, graph)

	g, gctx := errgroup.WithContext(ctx)

	// Build inter-stage channels. Each stage (except the last) produces to
	// output channels that a router reads from.

	stageIOs := make([]stageIO, len(stages))
	for s := range stages {
		p := parallelism
		stageIOs[s] = stageIO{
			inputChs:   make([]chan engine.Event, p),
			controlChs: make([]chan engine.ControlMsg, p),
			outputChs:  make([]chan engine.OutputMsg, p),
		}
		for i := 0; i < p; i++ {
			stageIOs[s].inputChs[i] = make(chan engine.Event, engine.DefaultInputBufferSize)
			stageIOs[s].controlChs[i] = make(chan engine.ControlMsg, 8)
			stageIOs[s].outputChs[i] = make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)
		}
	}

	// Launch each stage.
	for s, stage := range stages {
		s, stage := s, stage
		isFirst := (s == 0)
		isLast := (s == len(stages)-1)

		for i := 0; i < parallelism; i++ {
			idx := i
			g.Go(func() error {
				return ex.runStageInstance(gctx, stage, idx, isFirst, stageIOs[s], log)
			})
		}

		// Launch router between this stage and the next (if not last).
		if !isLast {
			nextIO := stageIOs[s+1]
			// Determine shuffle type from the edge connecting stages.
			shuffleType := ex.findShuffleBetween(stage, stages[s+1], graph)

			var routeFn func(engine.Event, int) int
			switch shuffleType {
			case ShuffleHash:
				routeFn = hashRouter()
			case ShuffleRebalance:
				routeFn = rebalanceRouter()
			default:
				routeFn = rebalanceRouter() // Default to round-robin.
			}

			// Collect upstream output channels and downstream input/control channels.
			upstreams := make([]<-chan engine.OutputMsg, parallelism)
			for i := 0; i < parallelism; i++ {
				upstreams[i] = stageIOs[s].outputChs[i]
			}
			downstreams := make([]chan<- engine.Event, parallelism)
			downCtrlChs := make([]chan<- engine.ControlMsg, parallelism)
			for i := 0; i < parallelism; i++ {
				downstreams[i] = nextIO.inputChs[i]
				downCtrlChs[i] = nextIO.controlChs[i]
			}

			router := &partitionRouter{
				upstreams:   upstreams,
				downstreams: downstreams,
				controlChs:  downCtrlChs,
				routeFn:     routeFn,
			}
			g.Go(func() error {
				router.run()
				return nil
			})
		}

		// For the last stage, drain output channels.
		if isLast {
			for i := 0; i < parallelism; i++ {
				outCh := stageIOs[s].outputChs[i]
				g.Go(func() error {
					for range outCh {
					}
					return nil
				})
			}
		}
	}

	err := g.Wait()
	if err == context.Canceled {
		err = nil
	}

	return &JobResult{
		JobID: jobName,
		Err:   err,
		Metrics: JobMetrics{
			Duration: time.Since(start),
		},
	}, err
}

// runStageInstance runs a single parallel instance of a pipeline stage.
func (ex *embeddedExecutor) runStageInstance(
	ctx context.Context,
	stage []*StreamNode,
	instanceIdx int,
	isSourceStage bool,
	io stageIO,
	log zerolog.Logger,
) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	var operators []engine.Operator
	var sourceOp engine.SourceOperator

	for _, node := range stage {
		switch node.Type {
		case NodeSource:
			if isSourceStage {
				sourceOp = &sourceAdapter{source: node.Source}
			}
		case NodeMap:
			operators = append(operators, &mapAdapter{fn: node.MapFn})
		case NodeFlatMap:
			operators = append(operators, &flatMapAdapter{fn: node.FlatMapFn})
		case NodeFilter:
			operators = append(operators, &filterAdapter{fn: node.FilterFn})
		case NodeSink:
			operators = append(operators, &sinkAdapter{sink: node.Sink})
		case NodeKeyBy:
			// KeyBy is a shuffle boundary — no operator needed (routing handles it).
		case NodeProcess:
			// For embedded mode, Process wraps to a FlatMapOperator.
			operators = append(operators, &processAdapter{fn: node.ProcessFn})
		case NodeWindow, NodeReduce:
			// Window/Reduce not yet implemented in embedded mode.
		}
	}

	eventCh := io.inputChs[instanceIdx]
	controlCh := io.controlChs[instanceIdx]
	outputCh := io.outputChs[instanceIdx]

	aligner := engine.NewBarrierAligner(1, engine.DefaultAlignmentBufferSize)
	metrics := engine.NoopCheckpointMetrics()
	errMetrics := engine.NoopErrorMetrics()
	chainLog := log.With().Int("instance", instanceIdx).Logger()

	ig, igctx := errgroup.WithContext(runCtx)

	if isSourceStage && sourceOp != nil {
		ig.Go(func() error {
			return engine.RunSourceReader(igctx, sourceOp, nil, eventCh, controlCh, chainLog)
		})
	}

	var producerWg sync.WaitGroup
	producerWg.Add(1)
	ig.Go(func() error {
		defer producerWg.Done()
		defer runCancel()
		return engine.RunOperatorChain(igctx, operators, eventCh, controlCh, outputCh, aligner, 1, metrics, chainLog, nil, nil, nil, nil, errMetrics)
	})

	go func() {
		producerWg.Wait()
		close(outputCh)
	}()

	err := ig.Wait()
	if err == context.Canceled {
		return nil
	}
	return err
}

// processAdapter wraps a ProcessFunc to implement engine.FlatMapOperator.
type processAdapter struct {
	fn ProcessFunc
}

func (a *processAdapter) Open(_ context.Context) error        { return nil }
func (a *processAdapter) Close() error                        { return nil }
func (a *processAdapter) Checkpoint(_ uint64) ([]byte, error) { return nil, nil }
func (a *processAdapter) FlatMap(_ context.Context, event engine.Event, emit func(engine.Event)) error {
	pctx := &embeddedProcessContext{key: event.Key}
	results, err := a.fn(pctx, event)
	if err != nil {
		return err
	}
	for _, e := range results {
		emit(e)
	}
	return nil
}

// embeddedProcessContext provides a basic ProcessContext for embedded mode.
type embeddedProcessContext struct {
	key    []byte
	values map[string]*mockValueState
	lists  map[string]*mockListState
	maps   map[string]*mockMapState
}

func (c *embeddedProcessContext) Key() []byte { return c.key }

func (c *embeddedProcessContext) GetValueState(name string) ValueState {
	if c.values == nil {
		c.values = make(map[string]*mockValueState)
	}
	if _, ok := c.values[name]; !ok {
		c.values[name] = &mockValueState{}
	}
	return c.values[name]
}

func (c *embeddedProcessContext) GetListState(name string) ListState {
	if c.lists == nil {
		c.lists = make(map[string]*mockListState)
	}
	if _, ok := c.lists[name]; !ok {
		c.lists[name] = &mockListState{}
	}
	return c.lists[name]
}

func (c *embeddedProcessContext) GetMapState(name string) MapState {
	if c.maps == nil {
		c.maps = make(map[string]*mockMapState)
	}
	if _, ok := c.maps[name]; !ok {
		c.maps[name] = &mockMapState{data: make(map[string][]byte)}
	}
	return c.maps[name]
}

// Compile-time check.
var _ engine.FlatMapOperator = (*processAdapter)(nil)

// splitStages splits the sorted nodes into stages, breaking at shuffle boundaries.
func (ex *embeddedExecutor) splitStages(sorted []*StreamNode, graph *StreamGraph) [][]*StreamNode {
	if len(sorted) == 0 {
		return nil
	}

	// Build set of node IDs that start a new stage (targets of shuffle edges).
	shuffleTargets := make(map[int]bool)
	for _, edge := range graph.edges {
		if edge.Shuffle == ShuffleHash || edge.Shuffle == ShuffleRebalance {
			shuffleTargets[edge.TargetID] = true
		}
	}

	var stages [][]*StreamNode
	var current []*StreamNode

	for _, node := range sorted {
		if shuffleTargets[node.ID] && len(current) > 0 {
			stages = append(stages, current)
			current = nil
		}
		current = append(current, node)
	}
	if len(current) > 0 {
		stages = append(stages, current)
	}

	return stages
}

// findShuffleBetween finds the shuffle type on the edge connecting two stages.
func (ex *embeddedExecutor) findShuffleBetween(stage1, stage2 []*StreamNode, graph *StreamGraph) ShuffleType {
	// Build sets of node IDs in each stage.
	s1 := make(map[int]bool)
	s2 := make(map[int]bool)
	for _, n := range stage1 {
		s1[n.ID] = true
	}
	for _, n := range stage2 {
		s2[n.ID] = true
	}

	for _, edge := range graph.edges {
		if s1[edge.SourceID] && s2[edge.TargetID] {
			return edge.Shuffle
		}
	}
	return ShuffleForward
}
