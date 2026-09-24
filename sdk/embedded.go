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

	// Topo-sort and check for shuffle boundaries.
	sorted := graph.topoSort()
	for _, node := range sorted {
		if _, ok := node.Sink.(TransactionalSink); ok {
			return nil, fmt.Errorf("sdk: transactional sink %q requires the cluster checkpoint runtime; embedded execution has no durable global checkpoint decisions", node.Name)
		}
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
		destination, err := engine.OpenDLQDestination(ctx, node.DLQSink, log.With().Str("operator", node.Name).Logger())
		if err != nil {
			return nil, err
		}
		ex.dlq[node.ID] = destination
		if sink, ok := node.DLQSink.(*sharedPipelineDLQSink); ok {
			shared[sink] = destination
		}
		defer destination.Close()
	}
	if ex.env.parallelism == 1 && graph.canFuseLinear() {
		return ex.runLinear(ctx, sorted, 1, jobName, start, log)
	}
	return ex.runGraph(ctx, sorted, jobName, start, log)
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
			sa := adaptSource(node.Source, node.TimestampExtractor)
			sourceOp = sa
		case NodeMap:
			operators = append(operators, &mapAdapter{fn: node.MapFn})
		case NodeFlatMap:
			operators = append(operators, &flatMapAdapter{fn: node.FlatMapFn})
		case NodeFilter:
			operators = append(operators, &filterAdapter{fn: node.FilterFn})
		case NodeSink:
			operators = append(operators, adaptSink(node.Sink))
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
				sourceOp = adaptSource(node.Source, node.TimestampExtractor)
			}
		case NodeMap:
			operators = append(operators, &mapAdapter{fn: node.MapFn})
		case NodeFlatMap:
			operators = append(operators, &flatMapAdapter{fn: node.FlatMapFn})
		case NodeFilter:
			operators = append(operators, &filterAdapter{fn: node.FilterFn})
		case NodeSink:
			operators = append(operators, adaptSink(node.Sink))
		case NodeKeyBy:
			// KeyBy is a shuffle boundary — no operator needed (routing handles it).
		case NodeProcess:
			// For embedded mode, Process wraps to a FlatMapOperator.
			operators = append(operators, &processAdapter{fn: node.ProcessFn, onTimer: node.TimerFn, sideTags: node.SideOutputTags, config: ex.env.stateBackend, nodeID: node.ID, instance: instanceIdx})
		case NodeWindow, NodeReduce:
			op, err := embeddedWindow(node)
			if err != nil {
				return err
			}
			if window, ok := op.(*engine.EventTimeWindowOperator); ok {
				window.StateBackendFactory = func() (engine.StateBackend, func(), error) { return ex.env.stateBackend.open(node.ID, instanceIdx) }
			}
			if window, ok := op.(interface{ SetMetricIdentity(string, string) }); ok {
				window.SetMetricIdentity(fmt.Sprintf("%s-%d", node.Name, node.ID), fmt.Sprintf("embedded/%d/%d", node.ID, instanceIdx))
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
	numKeyGroups     int
	backendFactory   func() (engine.StateBackend, func(), error)
	clock            func() time.Time
	fn               ProcessFunc
	onTimer          TimerFunc
	watermark        int64
	sideTags         []string
	config           StateBackendConfig
	nodeID, instance int
	backend          engine.StateBackend
	cleanup          func()
}

func (a *processAdapter) Open(_ context.Context) error {
	var err error
	if a.backendFactory != nil {
		a.backend, a.cleanup, err = a.backendFactory()
	} else {
		a.backend, a.cleanup, err = a.config.open(a.nodeID, a.instance)
	}
	if err == nil {
		if a.cleanup == nil {
			a.cleanup = func() {}
		}
		err = a.loadWatermark()
		if err != nil {
			_ = a.Close()
		}
	}
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
	if err := a.backend.Restore(handle); err != nil {
		return err
	}
	return a.loadWatermark()
}
func (a *processAdapter) FlatMap(ctx context.Context, event engine.Event, emit func(engine.Event)) error {
	original := a.backend
	transaction := newInvocationState(original)
	a.backend = transaction
	defer func() { a.backend = original }()
	pctx := a.processContext(event.Key, event.EventTime)
	results, err := a.fn(pctx, event)
	if err = errors.Join(err, pctx.err); err != nil {
		return err
	}
	if pctx.hasDueTimer {
		timers, err := a.fireDueTimers(ctx)
		if err != nil {
			return err
		}
		results = append(results, timers...)
	}
	if err := transaction.commit(); err != nil {
		return err
	}
	for _, e := range pctx.sideEvents {
		emit(e)
	}
	for _, e := range results {
		emit(e)
	}
	return nil
}

// Compile-time check.
var _ engine.FlatMapOperator = (*processAdapter)(nil)
var _ engine.StateHandleOperator = (*processAdapter)(nil)
