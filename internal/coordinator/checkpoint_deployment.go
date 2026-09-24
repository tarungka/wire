package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/keygroup"
	"github.com/tarungka/wire/internal/rpc"
)

// attachCheckpointRestoreLocked preserves the checkpoint's old epoch and
// replica locations separately from the new deployment's fencing token.
func (c *Coordinator) attachCheckpointRestoreLocked(job *JobMeta, assignments map[string][]rpc.TaskDescriptor) error {
	if job.RescaleCheckpoint > job.LatestCheckpoint {
		return fmt.Errorf("rescale checkpoint is not the selected recovery checkpoint")
	}
	if job.LatestCheckpoint == 0 {
		return nil
	}
	checkpoint, inventory, err := c.selectRecoveryCheckpointLocked(job)
	if err != nil {
		return err
	}
	if job.RestoreSavepoint != nil {
		var targets []rpc.TaskDescriptor
		for _, tasks := range assignments {
			targets = append(targets, tasks...)
		}
		sources, err := planSavepointTaskRestore(checkpoint, targets)
		if err != nil {
			return err
		}
		for _, tasks := range assignments {
			for i := range tasks {
				source := sources[tasks[i].TaskID]
				tasks[i].RestoreCheckpoint = &rpc.CheckpointRestoreDescriptor{SourceJobID: checkpoint.JobID, SourceTaskID: source, CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, ReplicaAddress: checkpoint.StatePaths[source], ArchiveSize: inventory[source].StateSizeBytes, ArchiveSHA256: inventory[source].StateSHA256["checkpoint.archive"]}
			}
		}
		return nil
	}
	if job.RescaleCheckpoint != 0 && job.RescaleCheckpoint == checkpoint.ID {
		if checkpoint.SavepointID == "" {
			return fmt.Errorf("rescale requires a savepoint")
		}
		savepoint, err := c.GetSavepoint(job.ID, checkpoint.SavepointID)
		if err != nil {
			return err
		}
		if savepoint.JobID != job.ID || savepoint.ID != checkpoint.SavepointID || savepoint.Status != SavepointCompleted || savepoint.CheckpointID != checkpoint.ID || savepoint.EpochID != checkpoint.EpochID || savepoint.NumKeyGroups != checkpoint.NumKeyGroups {
			return fmt.Errorf("rescale savepoint identity mismatch")
		}
		var targets []rpc.TaskDescriptor
		for _, tasks := range assignments {
			targets = append(targets, tasks...)
		}
		parts, err := planRescaleState(checkpoint, checkpoint.TaskDescriptors, targets)
		if err != nil {
			return err
		}
		for target, slices := range parts {
			for i := range slices {
				state := inventory[slices[i].SourceTaskID]
				slices[i].ArchiveSize = state.StateSizeBytes
				slices[i].ArchiveSHA256 = state.StateSHA256["checkpoint.archive"]
			}
			parts[target] = slices
		}
		for _, tasks := range assignments {
			for i := range tasks {
				tasks[i].RestoreCheckpoint = nil
				tasks[i].RestoreRescale = &rpc.RescaleRestoreDescriptor{CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, NumKeyGroups: checkpoint.NumKeyGroups, Parts: parts[tasks[i].TaskID]}
			}
		}
		return nil
	}
	var replacementIndexes map[string][]int
	if job.ReplacementCheckpoint != 0 && job.ReplacementCheckpoint == checkpoint.ID {
		var targets []rpc.TaskDescriptor
		for _, tasks := range assignments {
			targets = append(targets, tasks...)
		}
		if _, err := planTaskLayoutRestoreMode(checkpoint.NumKeyGroups, checkpoint.TaskDescriptors, targets, true); err != nil {
			return err
		}
		sources := make(map[string]rpc.TaskDescriptor, len(checkpoint.TaskDescriptors))
		for _, source := range checkpoint.TaskDescriptors {
			sources[source.TaskID] = source
		}
		replacementIndexes = make(map[string][]int, len(targets))
		for _, target := range targets {
			indexes, err := replacementChainIndexes(sources[target.TaskID].OperatorChain, target.OperatorChain)
			if err != nil {
				return err
			}
			replacementIndexes[target.TaskID] = indexes
		}
	}
	savedGroups := checkpoint.NumKeyGroups
	if savedGroups == 0 {
		savedGroups = keygroup.DefaultNumKeyGroups
	}
	count := 0
	for _, tasks := range assignments {
		for _, task := range tasks {
			taskGroups := task.NumKeyGroups
			if taskGroups == 0 {
				taskGroups = keygroup.DefaultNumKeyGroups
			}
			if taskGroups != savedGroups {
				return fmt.Errorf("key group count mismatch (checkpoint: %d, job: %d)", savedGroups, taskGroups)
			}
			count++
			if checkpoint.Tasks[task.TaskID] == "" || checkpoint.StatePaths[task.TaskID] == "" || checkpoint.StatePaths[task.TaskID] != checkpoint.Replicas[task.TaskID] {
				return fmt.Errorf("recovery checkpoint has no assigned replica for task %s", task.TaskID)
			}
		}
	}
	if count != len(checkpoint.Tasks) {
		return fmt.Errorf("recovery checkpoint task topology changed")
	}
	for _, tasks := range assignments {
		for i := range tasks {
			tasks[i].RestoreCheckpoint = &rpc.CheckpointRestoreDescriptor{OperatorRestoreIndexes: replacementIndexes[tasks[i].TaskID], CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, ReplicaAddress: checkpoint.StatePaths[tasks[i].TaskID], ArchiveSize: inventory[tasks[i].TaskID].StateSizeBytes, ArchiveSHA256: inventory[tasks[i].TaskID].StateSHA256["checkpoint.archive"]}
		}
	}
	return nil
}
