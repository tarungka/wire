package engine

import (
	"context"
	"encoding/json"
	"fmt"
)

func (ts *TaskSlot) restoreCheckpoint() error {
	snapshot := ts.RestoreCheckpoint
	if snapshot.TaskID != ts.TaskID || snapshot.CheckpointID == 0 || snapshot.HasSource != (ts.Source != nil) || len(snapshot.Operators) != len(ts.Operators) {
		return fmt.Errorf("restored checkpoint does not match task topology")
	}
	if err := snapshot.ValidateStateHandles(); err != nil {
		return err
	}
	typed := make(map[int]bool, len(snapshot.StateHandleIndexes))
	for _, index := range snapshot.StateHandleIndexes {
		typed[index] = true
	}
	restore := func(index int, operator Operator, data []byte) error {
		owned := append([]byte(nil), data...)
		return invokeOperator(func() error {
			if typed[index] {
				target, ok := operator.(StateHandleOperator)
				if !ok {
					return fmt.Errorf("operator %d cannot restore typed state", index)
				}
				var handle SnapshotHandle
				if err := json.Unmarshal(owned, &handle); err != nil {
					return err
				}
				return target.RestoreState(handle)
			}
			if target, ok := operator.(CheckpointRestorer); ok {
				return target.RestoreCheckpoint(owned)
			}
			if len(owned) != 0 {
				return fmt.Errorf("operator %d cannot restore nonempty checkpoint state", index)
			}
			return nil
		})
	}
	if ts.Source != nil {
		if err := restore(-1, ts.Source, snapshot.Source); err != nil {
			return err
		}
	}
	for index, operator := range ts.Operators {
		if err := restore(index, operator, snapshot.Operators[index]); err != nil {
			return err
		}
	}
	ts.RestoredCheckpointID = snapshot.CheckpointID
	return nil
}

// restoreSinkTransaction resolves the completed checkpoint's commit decision
// before RUNNING, BeginTransaction or source/input processing. Restoration is
// authorized only for a globally completed checkpoint, as selected by the
// coordinator; a replica receipt alone is not a commit decision.
func (ts *TaskSlot) restoreSinkTransaction(ctx context.Context) error {
	snapshot := ts.RestoreCheckpoint
	if snapshot == nil || !snapshot.SinkPrepared {
		return nil
	}
	if len(ts.Operators) == 0 {
		return fmt.Errorf("prepared sink checkpoint has no sink operator")
	}
	sink, ok := ts.Operators[len(ts.Operators)-1].(TransactionalSink)
	if !ok {
		return fmt.Errorf("prepared sink checkpoint requires a transactional sink")
	}
	if snapshot.SinkCommittedCheckpoint >= snapshot.CheckpointID {
		return fmt.Errorf("prepared checkpoint has inconsistent committed boundary")
	}
	return commitTransaction(ctx, sink, snapshot.CheckpointID)
}

func (ts *TaskSlot) recoverSinkTransactions(ctx context.Context) error {
	if ts.TransactionRecovery == nil || len(ts.Operators) == 0 {
		return nil
	}
	operator := ts.Operators[len(ts.Operators)-1]
	if _, ok := operator.(TransactionalSink); !ok {
		return nil
	}
	sink, ok := operator.(RecoverableTransactionalSink)
	if !ok {
		return fmt.Errorf("distributed transactional sink requires RecoverTransactions")
	}
	recovery := *ts.TransactionRecovery
	if recovery.JobID == "" || recovery.TaskID != ts.TaskID || recovery.DeploymentGeneration == 0 || recovery.EpochID == 0 || recovery.AttemptID == "" {
		return fmt.Errorf("transaction recovery requires fenced job/task identity")
	}
	if len(ts.RescaleState) > 0 || (ts.RestoredCheckpointID != 0 && ts.RestoreCheckpoint == nil) {
		return fmt.Errorf("transactional rescale requires recoverable transaction handle mapping")
	}
	recovery.CompletedCheckpointID = 0
	if ts.RestoreCheckpoint != nil {
		if !ts.RestoreCheckpoint.SinkPrepared {
			return fmt.Errorf("transactional restore requires a prepared sink checkpoint")
		}
		recovery.CompletedCheckpointID = ts.RestoreCheckpoint.CheckpointID
	}
	if err := invokeOperator(func() error { return sink.RecoverTransactions(ctx, recovery) }); err != nil {
		return fmt.Errorf("transaction orphan recovery: %w", err)
	}
	return nil
}
