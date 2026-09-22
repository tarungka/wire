package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/errorpolicy"
	"github.com/tarungka/wire/internal/logger"
)

// embeddedExecutor runs pipelines in-process.
type embeddedExecutor struct {
	env *StreamExecutionEnvironment
	dlq map[int]*engine.DLQDestination
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
	for _, node := range sorted {
		if node.NamedDLQ != nil {
			return nil, fmt.Errorf("sdk: named DLQ sinks require cluster mode")
		}
	}
	ex.dlq = make(map[int]*engine.DLQDestination)
	shared := make(map[*sharedPipelineDLQSink]*engine.DLQDestination)
	for _, node := range sorted {
		if node.DLQSink == nil {
			continue
		}
		if sink, ok := node.DLQSink.(*sharedPipelineDLQSink); ok {
			if destination := shared[sink]; destination != nil {
				ex.dlq[node.ID] = destination
				continue
			}
		}
		destination := engine.OpenDLQDestination(ctx, node.DLQSink, log.With().Str("operator", node.Name).Logger())
		ex.dlq[node.ID] = destination
		if sink, ok := node.DLQSink.(*sharedPipelineDLQSink); ok {
			shared[sink] = destination
		}
		defer destination.Close()
	}
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
	var errorConfigs []engine.ErrorHandlerConfig
	var sourceOp engine.SourceOperator

	for _, node := range sorted {
		before := len(operators)
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
		if len(operators) > before {
			cfg, err := errorpolicy.Compile(node.ErrorPolicy, node.Name)
			if err != nil {
				return err
			}
			if destination := ex.dlq[node.ID]; destination != nil {
				cfg.DLQWriter = destination.Write
			}
			errorConfigs = append(errorConfigs, cfg)
		}
	}

	if sourceOp == nil {
		return fmt.Errorf("sdk: no source in pipeline")
	}

	if err := sourceOp.Open(runCtx); err != nil {
		return err
	}
	defer func() { _ = sourceOp.Close() }()

	// Create channels.
	eventCh := make(chan engine.Event, engine.DefaultInputBufferSize)
	controlCh := make(chan engine.ControlMsg, 8)
	outputCh := make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)

	aligner := engine.NewBarrierAligner(1, engine.DefaultAlignmentBufferSize)
	metrics := engine.NoopCheckpointMetrics()
	errMetrics := engine.NewTelemetryErrorMetrics("")
	chainLog := log.With().Int("instance", instanceIdx).Logger()

	ig, igctx := errgroup.WithContext(runCtx)

	strategy, interval := embeddedWatermark(sorted)
	// Source reader goroutine.
	ig.Go(func() error {
		return engine.RunSourceReaderWithWatermarks(igctx, sourceOp, strategy, eventCh, controlCh, interval, chainLog)
	})

	// Operator chain goroutine.
	var producerWg sync.WaitGroup
	producerWg.Add(1)
	ig.Go(func() error {
		defer producerWg.Done()
		defer runCancel()
		return engine.RunOperatorChain(igctx, operators, eventCh, controlCh, outputCh, aligner, 1, metrics, chainLog, nil, nil, errorConfigs, nil, errMetrics)
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
			for _, node := range stage {
				if node.Type == NodeSource && node.Watermark != nil {
					router.idleTimeout = node.Watermark.IdleTimeout
					router.watermarkInterval = node.Watermark.EmitInterval
				}
			}
			if stages[s+1][0].Type == NodeKeyBy {
				router.keySelector = stages[s+1][0].KeyByFn
			}
			g.Go(func() error { return router.run(gctx) })
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
	var errorConfigs []engine.ErrorHandlerConfig
	var sourceOp engine.SourceOperator

	for _, node := range stage {
		before := len(operators)
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
			operators = append(operators, &processAdapter{fn: node.ProcessFn, config: ex.env.stateBackend, nodeID: node.ID, instance: instanceIdx})
		case NodeWindow, NodeReduce:
			op, err := embeddedWindow(node)
			if err != nil {
				return err
			}
			operators = append(operators, op)
		}
		if len(operators) > before {
			cfg, err := errorpolicy.Compile(node.ErrorPolicy, node.Name)
			if err != nil {
				return err
			}
			if destination := ex.dlq[node.ID]; destination != nil {
				cfg.DLQWriter = destination.Write
			}
			errorConfigs = append(errorConfigs, cfg)
		}
	}

	if isSourceStage && sourceOp != nil {
		if err := sourceOp.Open(runCtx); err != nil {
			return err
		}
		defer func() { _ = sourceOp.Close() }()
	}

	eventCh := io.inputChs[instanceIdx]
	controlCh := io.controlChs[instanceIdx]
	outputCh := io.outputChs[instanceIdx]

	aligner := engine.NewBarrierAligner(1, engine.DefaultAlignmentBufferSize)
	metrics := engine.NoopCheckpointMetrics()
	errMetrics := engine.NewTelemetryErrorMetrics("")
	chainLog := log.With().Int("instance", instanceIdx).Logger()

	ig, igctx := errgroup.WithContext(runCtx)

	strategy, interval := embeddedWatermark(stage)
	if isSourceStage && sourceOp != nil {
		ig.Go(func() error {
			return engine.RunSourceReaderWithWatermarks(igctx, sourceOp, strategy, eventCh, controlCh, interval, chainLog)
		})
	}

	var producerWg sync.WaitGroup
	producerWg.Add(1)
	ig.Go(func() error {
		defer producerWg.Done()
		defer runCancel()
		return engine.RunOperatorChain(igctx, operators, eventCh, controlCh, outputCh, aligner, 1, metrics, chainLog, nil, nil, errorConfigs, nil, errMetrics)
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
	fn               ProcessFunc
	config           StateBackendConfig
	nodeID, instance int
	backend          engine.StateBackend
	cleanup          func()
}

func (a *processAdapter) Open(_ context.Context) error {
	var err error
	a.backend, a.cleanup, err = a.config.open(a.nodeID, a.instance)
	return err
}
func (a *processAdapter) Close() error {
	if a.backend == nil {
		return nil
	}
	err := a.backend.Close()
	a.backend = nil
	a.cleanup()
	return err
}
func (a *processAdapter) Checkpoint(id uint64) ([]byte, error) {
	handle, err := a.CheckpointState(id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(handle)
}

func (a *processAdapter) CheckpointState(id uint64) (engine.SnapshotHandle, error) {
	if a.backend == nil {
		return engine.SnapshotHandle{}, engine.ErrBackendClosed
	}
	return a.backend.Checkpoint(id)
}

func (a *processAdapter) RestoreState(handle engine.SnapshotHandle) error {
	if a.backend == nil {
		return engine.ErrBackendClosed
	}
	return a.backend.Restore(handle)
}
func (a *processAdapter) FlatMap(_ context.Context, event engine.Event, emit func(engine.Event)) error {
	pctx := &backendProcessContext{key: append([]byte(nil), event.Key...), backend: a.backend}
	results, err := a.fn(pctx, event)
	if err = errors.Join(err, pctx.err); err != nil {
		return err
	}
	for _, e := range results {
		emit(e)
	}
	return nil
}

// Compile-time check.
var _ engine.FlatMapOperator = (*processAdapter)(nil)
var _ engine.StateHandleOperator = (*processAdapter)(nil)

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
