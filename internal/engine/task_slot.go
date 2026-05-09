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
	"github.com/tarungka/wire/internal/transport"
)

// TaskSlot represents a single execution slot in the stream processing
// topology. It orchestrates input readers, the operator chain, output writers,
// and optionally a source reader and watermark emitter.
type TaskSlot struct {
	Config       TaskSlotConfig
	Inputs       []*transport.FrameStream // Upstream input streams.
	Outputs      []*transport.FrameStream // Downstream output streams.
	Operators    []Operator               // Fused operator chain.
	Source       SourceOperator           // Non-nil for source tasks.
	Strategy     WatermarkStrategy        // Resolved watermark strategy (source tasks only).
	Coordinator  *CheckpointCoordinator   // Optional checkpoint coordinator (WIP-05).
	Metrics      CheckpointMetrics        // Optional checkpoint metrics collector.
	ErrorMetrics ErrorMetrics             // Optional error handling metrics collector (WIP-11).
	TaskIndex    int                      // Index of this task within the parallel subtasks.
	TaskID       string                   // Unique identifier for this task.
	log          zerolog.Logger
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
	// Create a cancellable context so we can shut everything down when
	// the operator chain finishes (whether success or failure).
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	g, gctx := errgroup.WithContext(runCtx)

	numInputs := len(ts.Inputs)
	if ts.Source != nil {
		numInputs = 1 // Source tasks have a virtual input.
	}

	// Create channels.
	eventCh := make(chan Event, ts.Config.InputBufferSize)
	controlCh := make(chan ControlMsg, numInputs*2+4) // barrier + EoP per input, +4 for 2PC control messages (CtrlCommitCheckpoint, CtrlAbortTransaction).
	outputCh := make(chan OutputMsg, ts.Config.OutputBufferSize)

	// Create barrier aligner.
	aligner := NewBarrierAligner(numInputs, ts.Config.AlignmentBufferSize)

	// Track output channel producers so we can close outputCh when all are done.
	var producerWg sync.WaitGroup

	// Close input streams when context is cancelled to unblock blocking I/O
	// in input readers. Output streams are left open so the output writer can
	// drain remaining messages (like the final EndOfPartition).
	go func() {
		<-gctx.Done()
		for _, s := range ts.Inputs {
			_ = s.Close()
		}
	}()

	// For source tasks, resolve strategy and launch source reader.
	if ts.Source != nil {
		strategy := ts.resolveStrategy()

		g.Go(func() error {
			return runSourceReader(gctx, ts.Source, strategy, eventCh, controlCh, ts.log.With().Str("component", "source_reader").Logger())
		})

		// Launch watermark emitter for source tasks.
		emitInterval := ts.resolveEmitInterval()
		producerWg.Add(1)
		g.Go(func() error {
			defer producerWg.Done()
			return runWatermarkEmitter(gctx, strategy, outputCh, emitInterval,
				ts.log.With().Str("component", "watermark_emitter").Logger())
		})
	} else if numInputs > 0 {
		// Create per-input watermark tracker (only for non-source tasks).
		tracker := NewInputWatermarkTracker(numInputs)

		// Launch input readers (one per upstream stream).
		for i, stream := range ts.Inputs {
			i, stream := i, stream
			g.Go(func() error {
				return runInputReader(gctx, i, stream, eventCh, controlCh, aligner, tracker,
					ts.log.With().Int("input", i).Logger())
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
			defer producerWg.Done()
			return runWatermarkPropagator(gctx, tracker, outputCh, emitInterval, idleTimeout,
				ts.log.With().Str("component", "watermark_propagator").Logger())
		})
	}

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
			return ts.Coordinator.Run(gctx)
		})
	}

	// Launch DLQ drain goroutine if DLQ is configured.
	if dlqCh != nil {
		dlqLog := ts.log.With().Str("component", "dlq").Logger()
		g.Go(func() error {
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
		defer producerWg.Done()
		defer runCancel() // Signal all goroutines to stop when chain exits.
		if dlqCh != nil {
			defer close(dlqCh)
		}
		err := runOperatorChain(gctx, ts.Operators, eventCh, controlCh, outputCh, aligner, numInputs, metrics, ts.log.With().Str("component", "operator_chain").Logger(), txnSink, ackFn, ts.Config.ErrorConfigs, dlqCh, errMetrics)
		if err != nil {
			chainErr.Store(&err)
		}
		return err
	})

	// Goroutine to close outputCh when all producers are done.
	go func() {
		producerWg.Wait()
		close(outputCh)
	}()

	// Launch output writers (one per downstream stream).
	for i, stream := range ts.Outputs {
		i, stream := i, stream
		g.Go(func() error {
			return runOutputWriter(gctx, stream, outputCh,
				ts.log.With().Int("output", i).Logger())
		})
	}

	err := g.Wait()
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
	for {
		batch, err := source.ReadBatch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
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
