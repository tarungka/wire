package coordinator

import (
	"fmt"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// attachCheckpointRestoreLocked preserves the checkpoint's old epoch and
// replica locations separately from the new deployment's fencing token.
func (c *Coordinator) attachCheckpointRestoreLocked(job *JobMeta, assignments map[string][]rpc.TaskDescriptor) error {
	if job.LatestCheckpoint == 0 {
		return nil
	}
	data, err := c.store.Get(CheckpointKey(job.ID, job.LatestCheckpoint))
	if err != nil {
		return err
	}
	var checkpoint CheckpointMeta
	if err := protocol.DecodeMsgPack(data, &checkpoint); err != nil {
		return err
	}
	if checkpoint.JobID != job.ID || checkpoint.ID != job.LatestCheckpoint || checkpoint.Status != CheckpointCompleted {
		return fmt.Errorf("recovery checkpoint is not completed for this job")
	}
	count := 0
	for _, tasks := range assignments {
		for _, task := range tasks {
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
			tasks[i].RestoreCheckpoint = &rpc.CheckpointRestoreDescriptor{CheckpointID: checkpoint.ID, EpochID: checkpoint.EpochID, ReplicaAddress: checkpoint.StatePaths[tasks[i].TaskID]}
		}
	}
	return nil
}
