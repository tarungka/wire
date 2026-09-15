package coordinator

import (
	"context"
	"fmt"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointInvalidationUsesCurrentRecoveryGrant(t *testing.T) {
	for _, mode := range []string{"valid", "old attempt", "wrong worker", "wrong task", "old epoch"} {
		t.Run(mode, func(t *testing.T) {
			c, store := checkpointPolicyCoordinator(t)
			cp := CheckpointMeta{ID: 7, EpochID: 2, JobID: "job", Status: CheckpointCompleted}
			assignment := TaskAssignmentMap{JobID: "job", AttemptID: "new", EpochID: 5, Assignments: map[string]string{"task": "worker"}, RestoreCheckpoints: map[string]rpc.CheckpointRestoreDescriptor{"task": {CheckpointID: 7, EpochID: 2}}}
			if err := store.Set(CheckpointKey("job", 7), encode(t, cp)); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
				t.Fatal(err)
			}
			req := rpc.UpdateTaskStatusRequest{JobID: "job", TaskID: "task", WorkerID: "worker", AttemptID: "new", EpochID: 5, Status: rpc.TaskStatusFailed, Failure: &rpc.TaskFailureInfo{ErrorClass: "checkpoint_unavailable", ErrorMessage: "missing archive"}}
			switch mode {
			case "old attempt":
				req.AttemptID = "old"
			case "wrong worker":
				req.WorkerID = "other"
			case "wrong task":
				req.TaskID = "other"
			case "old epoch":
				req.EpochID = 4
			}
			_, rpcErr := c.HandleUpdateTaskStatus(context.Background(), 1, encode(t, req))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			raw, err := store.Get(CheckpointKey("job", 7))
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.DecodeMsgPack(raw, &cp); err != nil {
				t.Fatal(err)
			}
			if (cp.InvalidReason != "") != (mode == "valid") {
				t.Fatalf("incorrect invalidation for %s: %s", mode, cp.InvalidReason)
			}
		})
	}
}

func TestInvalidCheckpointRefundsOnlyItsDeployment(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	job := c.jobs["job"]
	// More unavailable candidates than the default three execution attempts.
	for id := uint64(7); id > 2; id-- {
		job.Status = JobDeploying
		job.RecoveryAttempts = 2 // One prior execution failure plus this deployment.
		cp := CheckpointMeta{ID: id, EpochID: 2, JobID: "job", Status: CheckpointCompleted}
		assignment := TaskAssignmentMap{JobID: "job", AttemptID: fmt.Sprint(id), EpochID: 5,
			RecoveryAttemptCharged: true, Assignments: map[string]string{"task": "worker"},
			RestoreCheckpoints: map[string]rpc.CheckpointRestoreDescriptor{"task": {CheckpointID: id, EpochID: 2}}}
		if err := store.Set(CheckpointKey("job", id), encode(t, cp)); err != nil {
			t.Fatal(err)
		}
		if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
			t.Fatal(err)
		}
		req := rpc.UpdateTaskStatusRequest{JobID: "job", TaskID: "task", WorkerID: "worker", AttemptID: fmt.Sprint(id), EpochID: 5, Status: rpc.TaskStatusFailed, Failure: &rpc.TaskFailureInfo{ErrorClass: "checkpoint_unavailable", ErrorMessage: "missing archive"}}
		for repeat := 0; repeat < 2; repeat++ {
			if _, err := c.HandleUpdateTaskStatus(context.Background(), 1, encode(t, req)); err != nil {
				t.Fatal(err)
			}
			if job.RecoveryAttempts != 1 {
				t.Fatalf("attempt budget=%d", job.RecoveryAttempts)
			}
		}
		raw, err := store.Get(JobMetaKey("job"))
		if err != nil {
			t.Fatal(err)
		}
		var persisted JobMeta
		if err := protocol.DecodeMsgPack(raw, &persisted); err != nil {
			t.Fatal(err)
		}
		if persisted.RecoveryAttempts != 1 {
			t.Fatal("refund not persisted")
		}
	}
}
