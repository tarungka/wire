package worker

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// taskExecutor instantiates operators from a TaskDescriptor and runs them
// through the shared TaskSlot runtime. Operators are resolved by name from
// the worker registry rather than inline SDK function values.
type taskExecutor struct {
	reg *Registry
}

func newTaskExecutor(reg *Registry) *taskExecutor {
	return &taskExecutor{reg: reg}
}

// run builds the operator chain described by desc.OperatorChain, wires
// channels, and drives execution until ctx is cancelled or the source ends.
// Phase 1: single-input linear pipeline, no shuffle, no state, no checkpoints.
func (te *taskExecutor) run(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, log zerolog.Logger, onRunning func()) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = &engine.OperatorPanicError{Value: r, Stack: string(debug.Stack())}
		}
	}()
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

	slot := engine.NewTaskSlot(engine.DefaultTaskSlotConfig(), nil, nil, operators, sourceOp)
	slot.TaskID = taskID
	slot.TaskIndex = int(desc.SubtaskIndex)
	slot.OnRunning = onRunning
	return slot.Run(ctx)
}
