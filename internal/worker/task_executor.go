package worker

import (
	"context"
	"fmt"
	"sync"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// taskExecutor instantiates operators from a TaskDescriptor and runs them
// as a fused chain using the engine's operator-chain runtime. It mirrors the
// wiring of sdk/embedded.go:runLinearInstance but resolves operators by name
// from the worker's Registry instead of from inline SDK function values.
type taskExecutor struct {
	reg *Registry
}

func newTaskExecutor(reg *Registry) *taskExecutor {
	return &taskExecutor{reg: reg}
}

// run builds the operator chain described by desc.OperatorChain, wires
// channels, and drives execution until ctx is cancelled or the source ends.
// Phase 1: single-input linear pipeline, no shuffle, no state, no checkpoints.
func (te *taskExecutor) run(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, log zerolog.Logger) error {
	if len(desc.OperatorChain) == 0 {
		return fmt.Errorf("worker: task %q has empty OperatorChain", taskID)
	}

	tc := TaskContext{
		TaskID:       taskID,
		JobID:        jobID,
		OperatorID:   desc.OperatorID,
		SubtaskIndex: desc.SubtaskIndex,
		Parallelism:  desc.Parallelism,
		KeyGroup:     desc.KeyGroup,
		Log:          log,
	}

	var sourceOp engine.SourceOperator
	var operators []engine.Operator

	for i, od := range desc.OperatorChain {
		op, err := te.reg.Build(ctx, od, tc)
		if err != nil {
			return fmt.Errorf("worker: task %q operator[%d] %q: %w", taskID, i, od.OperatorID, err)
		}
		if od.Type == rpc.OperatorTypeSource {
			so, ok := op.(engine.SourceOperator)
			if !ok {
				return fmt.Errorf("worker: operator %q declared Source but factory returned %T", od.OperatorID, op)
			}
			if sourceOp != nil {
				return fmt.Errorf("worker: task %q has multiple sources in chain", taskID)
			}
			sourceOp = so
			continue
		}
		operators = append(operators, op)
	}

	if sourceOp == nil {
		return fmt.Errorf("worker: task %q has no source in OperatorChain", taskID)
	}

	// Wire channels identically to sdk/embedded.go:runLinearInstance.
	eventCh := make(chan engine.Event, engine.DefaultInputBufferSize)
	controlCh := make(chan engine.ControlMsg, 8)
	outputCh := make(chan engine.OutputMsg, engine.DefaultOutputBufferSize)
	aligner := engine.NewBarrierAligner(1, engine.DefaultAlignmentBufferSize)

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	g, gctx := errgroup.WithContext(runCtx)

	// Source goroutine.
	g.Go(func() error {
		return engine.RunSourceReader(gctx, sourceOp, nil, eventCh, controlCh, log)
	})

	// Operator chain goroutine. When it exits (either on context cancellation
	// or source end-of-partition), cancel the runCtx so the source reader
	// unblocks and close the output channel so the drainer returns.
	var producerWg sync.WaitGroup
	producerWg.Add(1)
	g.Go(func() error {
		defer producerWg.Done()
		defer runCancel()
		return engine.RunOperatorChain(
			gctx,
			operators,
			eventCh,
			controlCh,
			outputCh,
			aligner,
			1, // numInputs — Phase 1 single-input.
			engine.NoopCheckpointMetrics(),
			log,
			nil, // txnSink — Phase 1 no 2PC.
			nil, // ackFn — Phase 1 no checkpoint ack.
			nil, // errorConfigs
			nil, // dlqCh
			engine.NoopErrorMetrics(),
		)
	})

	// Close outputCh when the operator chain finishes so the drainer exits.
	go func() {
		producerWg.Wait()
		close(outputCh)
	}()

	// Linear pipelines have the sink in the chain; discard forwarded events.
	g.Go(func() error {
		for range outputCh {
		}
		return nil
	})

	err := g.Wait()
	if err == context.Canceled {
		return nil
	}
	return err
}
