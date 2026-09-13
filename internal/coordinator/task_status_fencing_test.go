package coordinator

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTaskStatusFencesEpochAndAssignment(t *testing.T) {
	for _, mode := range []string{"valid", "old-epoch", "wrong-worker", "wrong-job", "wrong-task", "follower", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
			c.taskStatuses["task"] = rpc.TaskStatusRunning
			assignment, err := protocol.EncodeMsgPack(TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(JobAssignmentsKey("job"), assignment); err != nil {
				t.Fatal(err)
			}
			req := rpc.UpdateTaskStatusRequest{WorkerID: "worker", JobID: "job", TaskID: "task", EpochID: 5, Status: rpc.TaskStatusFailed}
			switch mode {
			case "old-epoch":
				req.EpochID--
			case "wrong-worker":
				req.WorkerID = "other"
			case "wrong-job":
				req.JobID = "other"
			case "wrong-task":
				req.TaskID = "other"
			case "follower":
				c.recovered = false
			case "terminal":
				c.jobs["job"].Status = JobFinished
			}
			before := c.jobs["job"].Status
			payload, err := protocol.EncodeMsgPack(req)
			if err != nil {
				t.Fatal(err)
			}
			result, rpcErr := c.HandleUpdateTaskStatus(context.Background(), 1, payload)
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if result.(*rpc.UpdateTaskStatusResponse).Accepted != (mode == "valid") {
				t.Fatalf("response: %+v", result)
			}
			if mode == "valid" {
				if c.jobs["job"].Status != JobFailing || c.taskStatuses["task"] != rpc.TaskStatusFailed {
					t.Fatal("assigned failure not applied")
				}
			} else if c.jobs["job"].Status != before || c.taskStatuses["task"] != rpc.TaskStatusRunning || len(c.taskStatuses) != 1 {
				t.Fatal("rejected status mutated execution")
			}
		})
	}
}
