package coordinator

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointReplicaAuthorizationFencesAssignment(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-worker", "wrong-task", "stale-epoch", "aborted", "wrong-destination"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			checkpoint := CheckpointMeta{ID: 7, JobID: "job", EpochID: 5, Status: CheckpointInProgress, Tasks: map[string]string{"task": "source"}, Replicas: map[string]string{"task": "replica:1"}}
			request := rpc.AuthorizeCheckpointReplicaRequest{WorkerID: "replica", Snapshot: rpc.ReplicateCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 5, Size: 1}}
			switch mode {
			case "wrong-worker":
				request.WorkerID = "source"
			case "wrong-task":
				request.Snapshot.TaskID = "other"
			case "stale-epoch":
				request.Snapshot.EpochID = 4
			case "aborted":
				checkpoint.Status = CheckpointAborted
			case "wrong-destination":
				checkpoint.Replicas["task"] = "other:1"
			}
			stored, err := protocol.EncodeMsgPack(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(CheckpointKey("job", 7), stored); err != nil {
				t.Fatal(err)
			}
			payload, err := protocol.EncodeMsgPack(request)
			if err != nil {
				t.Fatal(err)
			}
			result, rpcErr := c.HandleAuthorizeCheckpointReplica(context.Background(), 1, payload)
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			response, ok := result.(*rpc.AcknowledgeCheckpointResponse)
			if !ok || response.Accepted != (mode == "valid") {
				t.Fatalf("authorization: %+v", result)
			}
		})
	}
}
