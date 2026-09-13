package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/transport"
)

// TaskSlot represents a single execution slot in the stream processing
// topology. It orchestrates input readers, the operator chain, output writers,
// and optionally a source reader and watermark emitter.
type TaskSlot struct {
	// CheckpointReport delivers checkpoint ID, epoch, and upload error to an
	// external coordinator. It must honor cancellation. Nil means report acceptance,
	// not global commit; commit and abort arrive through CheckpointDecisions.
	CheckpointReport func(context.Context, uint64, uint64, error) error
	// The caller owns this bounded channel and fences decisions to the execution.
	CheckpointDecisions  <-chan ControlMsg
	CheckpointTriggers   <-chan CheckpointTrigger // Optional source-only checkpoint commands.
	CheckpointReplicator CheckpointReplicator     // Requires Coordinator or CheckpointReport.
	Config               TaskSlotConfig
	Inputs               []*transport.FrameStream // Upstream input streams.
	Outputs              []*transport.FrameStream // Downstream output streams.
	Operators            []Operator               // Fused operator chain.
	Source               SourceOperator           // Non-nil for source tasks.
	Strategy             WatermarkStrategy        // Resolved watermark strategy (source tasks only).
	Coordinator          *CheckpointCoordinator   // Optional checkpoint coordinator (WIP-05).
	Metrics              CheckpointMetrics        // Optional checkpoint metrics collector.
	ErrorMetrics         ErrorMetrics             // Optional error handling metrics collector (WIP-11).
	TaskIndex            int                      // Index of this task within the parallel subtasks.
	RestoredCheckpointID uint64                   // Globally completed snapshot used for recovery.
	TaskID               string                   // Unique identifier for this task.
	OnRunning            func()                   // Called after all operators open, before any records are read.
	log                  zerolog.Logger
}

// NewTaskSlot creates a new TaskSlot with the given configuration.
func NewTaskSlot(cfg TaskSlotConfig, inputs []*transport.FrameStream, outputs []*transport.FrameStream, operators []Operator, source SourceOperator) *TaskSlot {
	return &TaskSlot{
		Config:    cfg,
		Inputs:    inputs,
		Outputs:   outputs,
		Operators: operators,
		Source:    source,
		log:       logger.GetLogger("task_slot"),
	}
}

// Run executes the task slot. It launches all goroutines via errgroup and
// blocks until completion or failure.
func (ts *TaskSlot) Run(ctx context.Context) error {
	if ts.CheckpointTriggers != nil && (ts.Source == nil || ts.CheckpointReplicator == nil) {
		return errors.New("source checkpoint triggers require a source and checkpoint replicator")
	}
	if ts.CheckpointReplicator != nil && ts.Coordinator == nil && ts.CheckpointReport == nil {
		return errors.New("checkpoint replication requires a coordinator")
	}
	if ts.Coordinator != nil && ts.CheckpointReport != nil {
		return errors.New("checkpoint reporting must select one coordinator")
	}
	if ts.Config.CheckpointUploadConcurrency < 0 {
		return errors.New("checkpoint upload concurrency must not be negative")
	}

	// Initialize synchronously so workers report RUNNING only after every
	// operator, including the source, has opened successfully.
	operators := make([]Operator, 0, len(ts.Operators)+1)
	if ts.Source != nil {
		operators = append(operators, ts.Source)
	}
	operators = append(operators, ts.Operators...)
	closeOperators, err := openOperators(ctx, operators, ts.log)
	if err != nil {
		return err
	}
	defer closeOperators()
	if err := ctx.Err(); err != nil {
		return err
	}
	if ts.OnRunning != nil {
		ts.OnRunning()
	}

	// Create a cancellable context so we can shut everything down when
	// the operator chain finishes (whether success or failure).
	runCtx, runCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer runCancel()
	var goroutines atomic.Int64
	runCtx = context.WithValue(runCtx, taskGoroutineKey{}, &goroutines)
	unregisterGoroutines, err := observability.ObserveTaskGoroutines(ts.TaskID, goroutines.Load)
	if err != nil {
		return err
	}
	defer func() { _ = unregisterGoroutines() }()

	g, gctx := errgroup.WithContext(runCtx)
	var checkpoint *chainCheckpointState
	if ts.CheckpointReplicator != nil {
		concurrency := ts.Config.CheckpointUploadConcurrency
		if concurrency == 0 {
			concurrency = 1
		}
		uploader, err := newCheckpointUploader(gctx, concurrency, ts.CheckpointReplicator)
		if err != nil {
			return err
		}
		if ts.Config.Checkpoint.Timeout > 0 {
			uploader.timeout = ts.Config.Checkpoint.Timeout
		}
		defer uploader.Close()
		checkpoint = &chainCheckpointState{uploader: uploader, taskID: ts.TaskID, pending: make(map[checkpointIdentity]bool), notify: func(ctx context.Context, r checkpointUploadResult) error {
			if ts.CheckpointReport != nil {
				return ts.CheckpointReport(ctx, r.CheckpointID, r.EpochID, r.Err)
			}
			if r.Err != nil {
				return ts.Coordinator.FailCheckpoint(ctx, r.CheckpointID, r.EpochID, r.Err)
			}
			return ts.Coordinator.AckReplicatedCheckpoint(ctx, ts.TaskIndex, r.CheckpointID, r.EpochID)
		}}
	}
	intakeCtx, stopIntake := context.WithCancel(gctx)
	defer stopIntake()
	normalizeIntake := func(err error) error {
		if errors.Is(err, context.Canceled) && intakeCtx.Err() != nil {
			return nil
		}
		return err
	}
	// A successful chain cancels input readers, but queued output still needs
	// to drain through downstream backpressure. External cancellation and real
	// failures interrupt paused writers; external cancellation allows bounded draining.
	outputCtx, cancelOutput := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelOutput()
	drainTimeout := ts.Config.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = DefaultDrainTimeout
	}
	// External cancellation stops intake; processing and output get one shared
	// drain budget. Real task failures still cancel the processing group.
	var drainMu sync.Mutex
	var drainTimer *time.Timer
	drainStarted := make(chan struct{})
	stopDrain := context.AfterFunc(ctx, func() {
		defer taskGoroutineStarted(runCtx)()
		defer close(drainStarted)
		stopIntake()
		drainMu.Lock()
		drainTimer = time.AfterFunc(drainTimeout, func() { defer taskGoroutineStarted(runCtx)(); runCancel(); cancelOutput() })
		drainMu.Unlock()
	})
	defer func() {
		if !stopDrain() {
			<-drainStarted
		}
		drainMu.Lock()
		if drainTimer != nil {
			drainTimer.Stop()
		}
		drainMu.Unlock()
	}()
	var chainSucceeded atomic.Bool
	stopOutput := context.AfterFunc(gctx, func() {
		defer taskGoroutineStarted(runCtx)()
		if !chainSucceeded.Load() {
			cancelOutput()
		}
	})
	defer stopOutput()

	numInputs := len(ts.Inputs)
	if ts.Source != nil {
		numInputs = 1 // Source tasks have a virtual input.
	}

	// Create channels.
	eventCh := make(chan Event, ts.Config.InputBufferSize)
	controlCh := make(chan ControlMsg, numInputs*2+4) // barrier + EoP per input, +4 for 2PC control messages (CtrlCommitCheckpoint, CtrlAbortTransaction).
	outputCh := make(chan OutputMsg, ts.Config.OutputBufferSize)
	recordBackpressure, err := observability.TaskBackpressureRecorder(ts.TaskID)
	if err != nil {
		return err
	}
	chainCtx := context.WithValue(gctx, taskBackpressureKey{}, recordBackpressure)
	aligner := NewBarrierAligner(numInputs, ts.Config.AlignmentBufferSize)
	unregisterChannels, err := observability.ObserveTaskChannels(ts.TaskID, func() (int, int) {
		return len(eventCh), len(outputCh)
	}, aligner.BufferedBytes)
	if err != nil {
		return err
	}
	defer func() {
		if err := unregisterChannels(); err != nil {
			ts.log.Warn().Err(err).Msg("unregister task channel metrics")
		}
	}()
	if checkpoint != nil && ts.Coordinator != nil {
		if err := ts.Coordinator.BindTaskControl(ts.TaskIndex, controlCh); err != nil {
			return err
		}
	}
	if ts.CheckpointDecisions != nil {
		g.Go(func() error {
			defer taskGoroutineStarted(gctx)()
			for {
				select {
				case <-gctx.Done():
					return nil
				case decision, ok := <-ts.CheckpointDecisions:
					if !ok {
						return nil
					}
					if decision.Type != CtrlCommitCheckpoint && decision.Type != CtrlAbortCheckpoint && decision.Type != CtrlAbortTransaction {
						return errors.New("invalid external checkpoint decision")
					}
					select {
					case controlCh <- decision:
					case <-gctx.Done():
						return nil
					}
				}
			}
		})
	}

	// Track output channel producers so we can close outputCh when all are done.
	var producerWg sync.WaitGroup
	var inputWg sync.WaitGroup

	var helperWg sync.WaitGroup
	helperWg.Add(2)
	// Close input streams when context is cancelled to unblock blocking I/O
	// in input readers. Output streams are left open so the output writer can
	// drain remaining messages (like the final EndOfPartition).
	go func() {
		defer taskGoroutineStarted(runCtx)()
		defer helperWg.Done()
		<-intakeCtx.Done()
		for _, s := range ts.Inputs {
			_ = s.Close()
		}
	}()

	// For source tasks, resolve strategy and launch source reader.
	if ts.Source != nil {
		strategy := ts.resolveStrategy()
		sourceCheckpoints := &sourceCheckpointInput{requests: ts.CheckpointTriggers, source: ts.Source, aligner: aligner, control: controlCh}

		inputWg.Add(1)
		g.Go(func() error {
			defer taskGoroutineStarted(runCtx)()
			defer inputWg.Done()
			return normalizeIntake(invokeOperator(func() error {
				return runSourceReaderWithContexts(intakeCtx, gctx, ts.Source, strategy, eventCh, controlCh, ts.log.With().Str("component", "source_reader").Logger(), sourceCheckpoints)
			}))
		})

		// Launch watermark emitter for source tasks.
		emitInterval := ts.resolveEmitInterval()
		producerWg.Add(1)
		g.Go(func() error {
			defer taskGoroutineStarted(runCtx)()
			defer producerWg.Done()
			return normalizeIntake(invokeOperator(func() error {
				return runWatermarkEmitter(intakeCtx, strategy, outputCh, emitInterval,
					ts.log.With().Str("component", "watermark_emitter").Logger())
			}))
		})
	} else if numInputs > 0 {
		// Create per-input watermark tracker (only for non-source tasks).
		tracker := NewInputWatermarkTracker(numInputs)

		// Launch input readers (one per upstream stream).
		for i, stream := range ts.Inputs {
			i, stream := i, stream
			stream.MarkCheckpointCompleted(ts.RestoredCheckpointID)
			if ts.Coordinator != nil {
				stream.SetCheckpointCompletionReader(ts.Coordinator.LastCompletedCheckpoint)
			}
			inputWg.Add(1)
			g.Go(func() error {
				defer taskGoroutineStarted(runCtx)()
				defer inputWg.Done()
				return runInputReaderWithContexts(intakeCtx, gctx, i, stream, eventCh, controlCh, aligner, tracker,
					ts.log.With().Int("input", i).Logger(), stream.ReportBufferUsage)
			})
		}

		// Launch watermark propagator for non-source tasks.
		emitInterval := ts.resolveEmitInterval()
		// IdleTimeout=0 means "use default". Users who want to disable idle
		// detection should leave it unconfigured and rely on the zero-timeout
		// semantics in InputWatermarkTracker.MinWatermark (all inputs participate).
		idleTimeout := ts.Config.Watermark.IdleTimeout
		if idleTimeout == 0 {
			idleTimeout = DefaultIdleTimeout
		}
		producerWg.Add(1)
		g.Go(func() error {
			defer taskGoroutineStarted(runCtx)()
			defer producerWg.Done()
			return normalizeIntake(runWatermarkPropagator(intakeCtx, tracker, outputCh, emitInterval, idleTimeout,
				ts.log.With().Str("component", "watermark_propagator").Logger()))
		})
	}

	// Stop alignment before waiting for intake: a full side buffer may be
	// holding a reader. Final shutdown is queued only after dispatch completes.
	helperWg.Add(1)
	go func() {
		defer taskGoroutineStarted(runCtx)()
		defer helperWg.Done()
		select {
		case <-ctx.Done():
		case <-gctx.Done():
			return
		}
		select {
		case controlCh <- ControlMsg{Type: CtrlDrainInputs}:
		case <-gctx.Done():
			return
		}
		inputWg.Wait()
		select {
		case controlCh <- ControlMsg{Type: CtrlShutdown}:
		case <-gctx.Done():
		}
	}()

	// Resolve checkpoint metrics.
	metrics := ts.Metrics
	if metrics == nil {
		metrics = NoopCheckpointMetrics()
	}

	// Resolve error metrics.
	errMetrics := ts.ErrorMetrics
	if errMetrics == nil {
		errMetrics = NoopErrorMetrics()
	}

	// Create DLQ channel if error configs are configured.
	var dlqCh chan DLQEvent
	if ts.Config.ErrorConfigs != nil {
		bufSize := ts.Config.DLQBufferSize
		if bufSize <= 0 {
			bufSize = DefaultDLQBufferSize
		}
		dlqCh = make(chan DLQEvent, bufSize)
	}

	// Detect if the last operator is a TransactionalSink.
	var txnSink TransactionalSink
	if len(ts.Operators) > 0 {
		txnSink, _ = ts.Operators[len(ts.Operators)-1].(TransactionalSink)
	}

	// Register transactional sink with coordinator if applicable.
	if txnSink != nil && ts.Coordinator != nil {
		ts.Coordinator.RegisterTransactionalSink(ts.TaskIndex, ts.TaskID)
	}

	// Build ackFn closure for transactional sinks.
	var ackFn func(checkpointID uint64)
	if txnSink != nil && ts.Coordinator != nil {
		taskIndex := ts.TaskIndex
		ackFn = func(checkpointID uint64) {
			ts.Coordinator.AckCheckpoint(taskIndex, checkpointID)
		}
	}

	// Launch checkpoint coordinator if configured.
	if ts.Coordinator != nil {
		g.Go(func() error {
			defer taskGoroutineStarted(runCtx)()
			return ts.Coordinator.Run(gctx)
		})
	}

	// Launch DLQ drain goroutine if DLQ is configured.
	if dlqCh != nil {
		dlqLog := ts.log.With().Str("component", "dlq").Logger()
		g.Go(func() error {
			defer taskGoroutineStarted(runCtx)()
			for dlqEvent := range dlqCh {
				dlqLog.Error().
					Str("operator", dlqEvent.OperatorName).
					Str("error", dlqEvent.Error).
					Int("retries", dlqEvent.RetryCount).
					Msg("event routed to DLQ")
			}
			return nil
		})
	}

	// Launch operator chain (the main processing goroutine).
	// When it finishes, cancel the run context to shut down all other goroutines.
	//
	// chainErr captures the chain's terminal error before runCancel()
	// propagates: errgroup picks the FIRST non-nil error any goroutine
	// returns, and the chain's defer runCancel() can race other
	// goroutines into returning context.Canceled before the chain's
	// own ErrOperatorPanic reaches errOnce.Do. Storing the chain error
	// in straight-line code (before any defers fire) makes the
	// chain's verdict authoritative regardless of who wins the
	// errgroup race. See docs/trds/WIP-24.
	var chainErr atomic.Pointer[error]
	producerWg.Add(1)
	g.Go(func() error {
		defer taskGoroutineStarted(runCtx)()
		defer producerWg.Done()
		defer runCancel() // Signal all goroutines to stop when chain exits.
		if dlqCh != nil {
			defer close(dlqCh)
		}
		err := runOpenedOperatorChain(chainCtx, ts.Operators, eventCh, controlCh, outputCh, aligner, numInputs, metrics, ts.log.With().Str("component", "operator_chain").Logger(), txnSink, ackFn, ts.Config.ErrorConfigs, dlqCh, errMetrics, checkpoint)
		if err != nil {
			chainErr.Store(&err)
		} else {
			chainSucceeded.Store(true)
		}
		return err
	})

	// Goroutine to close outputCh when all producers are done.
	go func() {
		defer taskGoroutineStarted(runCtx)()
		defer helperWg.Done()
		producerWg.Wait()
		close(outputCh)
	}()

	// A single dispatcher preserves record/control ordering and broadcasts
	// control frames to every downstream stream. It also drains terminal chains.
	g.Go(func() error {
		defer taskGoroutineStarted(runCtx)()
		return runOutputRouter(outputCtx, ts.Outputs, outputCh, ts.log)
	})

	err = g.Wait()
	helperWg.Wait()
	// Prefer the chain's error over errgroup's verdict — but only when
	// it's a real chain-side error (e.g. ErrOperatorPanic), not a
	// context.Canceled produced because a peer goroutine errored first
	// and errgroup cancelled gctx underneath us. errgroup returns
	// whichever goroutine's error wins errOnce.Do; the chain's
	// runCancel() defer can lose that race to a peer returning
	// context.Canceled, which would otherwise mask the chain's error.
	// Conversely, if the chain bowed out because a peer errored, that
	// peer's error is what should bubble up — defer to errgroup.
	if e := chainErr.Load(); e != nil && !errors.Is(*e, context.Canceled) {
		return *e
	}
	// Filter out context.Canceled — this is expected when the operator chain
	// finishes cleanly and triggers cancellation of the group.
	if err == context.Canceled {
		return nil
	}
	return err
}

// resolveStrategy creates the appropriate WatermarkStrategy based on config.
// Falls back to legacySourceStrategy wrapping Source.GenerateWatermark().
func (ts *TaskSlot) resolveStrategy() WatermarkStrategy {
	if ts.Strategy != nil {
		return ts.Strategy
	}

	switch ts.Config.Watermark.Strategy {
	case StrategyBoundedOOO:
		maxOOO := ts.Config.Watermark.MaxOOO
		if maxOOO <= 0 {
			maxOOO = DefaultMaxOOO
		}
		return NewBoundedOutOfOrdernessStrategy(maxOOO)
	case StrategyMonotonic:
		return NewMonotonicTimestampsStrategy()
	case StrategyIngestionTime:
		return NewIngestionTimeStrategy()
	default:
		// Legacy: wrap the source's GenerateWatermark() method.
		return newLegacySourceStrategy(ts.Source)
	}
}

// resolveEmitInterval returns the watermark emission interval, preferring
// Watermark.EmitInterval over the legacy WatermarkInterval.
func (ts *TaskSlot) resolveEmitInterval() time.Duration {
	if ts.Config.Watermark.EmitInterval > 0 {
		return ts.Config.Watermark.EmitInterval
	}
	if ts.Config.WatermarkInterval > 0 {
		return ts.Config.WatermarkInterval
	}
	return DefaultWatermarkInterval
}

// runSourceReader reads batches from a SourceOperator and feeds events into
// the eventCh. For source tasks, this replaces the input readers.
// If a WatermarkStrategy is provided, ObserveEventTime is called for each event.
func runSourceReader(ctx context.Context, source SourceOperator, strategy WatermarkStrategy, eventCh chan<- Event, controlCh chan<- ControlMsg, log zerolog.Logger) error {
	return runSourceReaderWithContexts(ctx, ctx, source, strategy, eventCh, controlCh, log)
}

// Stop fetching when intake is cancelled, but drain an already-fetched batch
// using the processing context. A source must honor its ReadBatch context.
func runSourceReaderWithContexts(intakeCtx, ctx context.Context, source SourceOperator, strategy WatermarkStrategy, eventCh chan<- Event, controlCh chan<- ControlMsg, log zerolog.Logger, checkpoints ...*sourceCheckpointInput) error {
	for {
		if err := intakeCtx.Err(); err != nil {
			return err
		}
		if len(checkpoints) > 0 {
			if err := checkpoints[0].atBoundary(intakeCtx, ctx); err != nil {
				return err
			}
		}
		batch, err := source.ReadBatch(intakeCtx)
		if err != nil {
			if intakeCtx.Err() != nil {
				return intakeCtx.Err()
			}
			log.Error().Err(err).Msg("source read batch error")
			return err
		}

		if batch == nil {
			// End of source input.
			ctrl := ControlMsg{
				Type:       CtrlEndOfPartition,
				InputIndex: 0,
			}
			select {
			case controlCh <- ctrl:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		}

		for _, event := range batch {
			if strategy != nil {
				strategy.ObserveEventTime(event.EventTime)
			}
			select {
			case eventCh <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
