package coordinator

import (
	"context"
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
