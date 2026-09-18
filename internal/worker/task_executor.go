package worker

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
	"github.com/tarungka/wire/internal/transport"
)

// taskExecutor instantiates operators from a TaskDescriptor and runs them
// through the shared TaskSlot runtime. Operators are resolved by name from
// the worker registry rather than inline SDK function values.
type taskExecutor struct {
	taskConfig *engine.TaskSlotConfig
	reg        *Registry
	data       *transport.Mux
}

func newTaskExecutor(reg *Registry) *taskExecutor {
	return &taskExecutor{reg: reg}
}

// run builds the operator chain described by desc.OperatorChain, wires
// channels, and drives execution until ctx is cancelled or the source ends.
// Explicit upstream/downstream descriptors connect separate worker tasks.
func (te *taskExecutor) run(ctx context.Context, jobID, taskID string, desc rpc.TaskDescriptor, log zerolog.Logger, onRunning func(), checkpoints ...*taskCheckpointRuntime) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = &engine.OperatorPanicError{Value: r, Stack: string(debug.Stack())}
		}
	}()
	if len(desc.OperatorChain) == 0 {
		return fmt.Errorf("worker: task %q has empty OperatorChain", taskID)
	}

	watermark, err := taskWatermarkConfig(desc.OperatorChain)
	if err != nil {
		return err
	}
	groups := desc.NumKeyGroups
	if groups == 0 {
		groups = keygroup.DefaultNumKeyGroups
	}
	tc := TaskContext{
		DeploymentGeneration: desc.DeploymentGeneration,
		EpochID:              desc.EpochID,
		AttemptID:            desc.AttemptID,
		NumKeyGroups:         groups,
		TaskID:               taskID,
		JobID:                jobID,
		OperatorID:           desc.OperatorID,
		SubtaskIndex:         desc.SubtaskIndex,
		Parallelism:          desc.Parallelism,
		KeyGroup:             desc.KeyGroup,
		Log:                  log,
	}

	var sourceOp engine.SourceOperator
	var operators []engine.Operator
	var errorConfigs []engine.ErrorHandlerConfig

	// Validate every policy before invoking user factories.
	for _, od := range desc.OperatorChain {
		if od.DLQSink != nil && (od.DLQSink.ClassName == "" || od.ErrorPolicy == nil || od.ErrorPolicy.OnExhausted != "dlq") {
			return fmt.Errorf("worker: invalid DLQ configuration for %q", od.OperatorID)
		}
		if od.Type == rpc.OperatorTypeSource && od.ErrorPolicy != nil {
			return fmt.Errorf("worker: source error policies are not supported")
		}
		if _, err := compileErrorPolicy(od.ErrorPolicy, od.OperatorID); err != nil {
			return err
		}
	}

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
		cfg, err := compileErrorPolicy(od.ErrorPolicy, od.OperatorID)
		if err != nil {
			return err
		}
		if od.DLQSink != nil {
			dlqOp, err := te.reg.Build(ctx, rpc.OperatorDescriptor{OperatorID: od.OperatorID + "/dlq", Type: rpc.OperatorTypeSink, ClassName: od.DLQSink.ClassName, Config: od.DLQSink.Config}, tc)
			if err != nil {
				return fmt.Errorf("worker: build DLQ: %w", err)
			}
			sink, ok := dlqOp.(engine.SinkOperator)
			if !ok {
				return fmt.Errorf("worker: DLQ factory returned %T", dlqOp)
			}
			if err := sink.Open(ctx); err != nil {
				return fmt.Errorf("worker: open DLQ: %w", err)
			}
			defer sink.Close()
			cfg.DLQWriter = func(e engine.DLQEvent) error {
				data, err := engine.MarshalDLQEvent(e)
				if err != nil {
					return err
				}
				return sink.Write(ctx, engine.Event{Key: e.OriginalEvent.Key, Value: data, EventTime: e.Timestamp})
			}
		}
		errorConfigs = append(errorConfigs, cfg)
	}

	if sourceOp == nil && len(desc.Upstream) == 0 {
		return fmt.Errorf("worker: task %q has no source in OperatorChain", taskID)
	}

	if sourceOp != nil && len(desc.Upstream) > 0 {
		return fmt.Errorf("worker: task cannot combine a local source with network inputs")
	}
	inputs, outputs, cleanup, err := connectTaskStreams(ctx, te.data, jobID, taskID, desc)
	if err != nil {
		return err
	}
	defer cleanup()
	config := engine.DefaultTaskSlotConfig()
	if te.taskConfig != nil {
		config = *te.taskConfig
	}
	config.ErrorConfigs = errorConfigs
	if watermark != nil {
		config.Watermark = *watermark
	}
	slot := engine.NewTaskSlot(config, inputs, outputs, operators, sourceOp)
	slot.TaskID = taskID
	slot.TransactionRecovery = &engine.TransactionRecovery{DeploymentGeneration: desc.DeploymentGeneration, JobID: jobID, TaskID: taskID, EpochID: desc.EpochID, AttemptID: desc.AttemptID}
	for _, upstream := range desc.Upstream {
		slot.InputIdleTimeouts = append(slot.InputIdleTimeouts, upstream.IdleTimeout)
	}
	slot.OutputKeyGroups = desc.OutputKeyGroups
	slot.TaskIndex = int(desc.SubtaskIndex)
	slot.OnRunning = onRunning
	if len(checkpoints) > 0 && checkpoints[0] != nil {
		checkpoint := checkpoints[0]
		slot.RestoreCheckpoint = checkpoint.restore
		slot.RescaleState = checkpoint.rescale
		slot.RestoredCheckpointID = checkpoint.restoredID
		slot.CheckpointReplicator = checkpoint.replicator
		slot.CheckpointReport = checkpoint.report
		slot.CheckpointDecisions = checkpoint.decisions
		if sourceOp != nil && checkpoint.replicator != nil {
			slot.CheckpointTriggers = checkpoint.triggers
			slot.SourceExhausted = checkpoint.sourceExhausted
		}
	}
	return slot.Run(ctx)
}
