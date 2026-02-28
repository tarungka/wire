package engine

import (
	"context"
	"sync"
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
	Config    TaskSlotConfig
	Inputs    []*transport.FrameStream // Upstream input streams.
	Outputs   []*transport.FrameStream // Downstream output streams.
	Operators []Operator               // Fused operator chain.
	Source    SourceOperator           // Non-nil for source tasks.
	Strategy  WatermarkStrategy        // Resolved watermark strategy (source tasks only).
	log       zerolog.Logger
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
	controlCh := make(chan ControlMsg, numInputs*2) // Extra capacity for barrier + EoP per input.
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
			s.Close()
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

	// Launch operator chain (the main processing goroutine).
	// When it finishes, cancel the run context to shut down all other goroutines.
	producerWg.Add(1)
	g.Go(func() error {
		defer producerWg.Done()
		defer runCancel() // Signal all goroutines to stop when chain exits.
		return runOperatorChain(gctx, ts.Operators, eventCh, controlCh, outputCh, aligner, numInputs, ts.log.With().Str("component", "operator_chain").Logger())
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
	// Filter out context.Canceled — this is expected when the operator chain
	// finishes and triggers cancellation of the group.
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
