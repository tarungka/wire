package engine

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/transport"
	"golang.org/x/sync/errgroup"
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

	// Shared watermark.
	var watermark atomic.Int64

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

	// Launch input readers (one per upstream stream).
	for i, stream := range ts.Inputs {
		i, stream := i, stream
		g.Go(func() error {
			return runInputReader(gctx, i, stream, eventCh, controlCh, aligner, &watermark,
				ts.log.With().Int("input", i).Logger())
		})
	}

	// For source tasks, launch source reader.
	if ts.Source != nil {
		g.Go(func() error {
			return runSourceReader(gctx, ts.Source, eventCh, controlCh, ts.log.With().Str("component", "source_reader").Logger())
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

	// For source tasks, launch watermark emitter.
	if ts.Source != nil {
		producerWg.Add(1)
		g.Go(func() error {
			defer producerWg.Done()
			return runWatermarkEmitter(gctx, ts.Source, &watermark, outputCh, ts.Config.WatermarkInterval,
				ts.log.With().Str("component", "watermark_emitter").Logger())
		})
	}

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

// runSourceReader reads batches from a SourceOperator and feeds events into
// the eventCh. For source tasks, this replaces the input readers.
func runSourceReader(ctx context.Context, source SourceOperator, eventCh chan<- Event, controlCh chan<- ControlMsg, log zerolog.Logger) error {
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
			select {
			case eventCh <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
