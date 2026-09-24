package coordinator

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointReplicaAuthorizationFencesAssignment(t *testing.T) {
	for _, mode := range []string{"valid", "verified-source", "wrong-source", "wrong-worker", "wrong-task", "stale-epoch", "aborted", "wrong-destination"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			checkpoint := CheckpointMeta{ID: 7, JobID: "job", EpochID: 5, Status: CheckpointInProgress, Tasks: map[string]string{"task": "source"}, Replicas: map[string]string{"task": "replica:1"}}
			request := rpc.AuthorizeCheckpointReplicaRequest{WorkerID: "replica", Snapshot: rpc.ReplicateCheckpointRequest{JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 5, Size: 1}}
			switch mode {
			case "verified-source":
				request.SourceWorkerID = "source"
			case "wrong-source":
				request.SourceWorkerID = "impostor"
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
			if !ok || response.Accepted != (mode == "valid" || mode == "verified-source") {
				t.Fatalf("authorization: %+v", result)
			}
		})
	}
}

func TestCheckpointFetchAuthorization(t *testing.T) {
	for _, mode := range []string{"valid-old-snapshot", "stale-deployment", "old-attempt", "wrong-reader", "wrong-replica", "uncommitted", "wrong-snapshot-epoch", "not-latest", "wrong-state-path"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.jobs["job"] = &JobMeta{ID: "job", Status: JobDeploying, LatestCheckpoint: 7}
			c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			cp := CheckpointMeta{ID: 7, JobID: "job", EpochID: 2, Status: CheckpointCompleted, Tasks: map[string]string{"task": "old-worker"}, Replicas: map[string]string{"task": "replica:1"}, StatePaths: map[string]string{"task": "replica:1"}}
			assignment := TaskAssignmentMap{AttemptID: "current", JobID: "job", Assignments: map[string]string{"task": "new-worker"}}
			request := rpc.AuthorizeCheckpointFetchRequest{ReplicaWorkerID: "replica", Fetch: rpc.FetchCheckpointRequest{AttemptID: "current", WorkerID: "new-worker", DeploymentEpoch: 5, JobID: "job", TaskID: "task", CheckpointID: 7, EpochID: 2}}
			switch mode {
			case "old-attempt":
				request.Fetch.AttemptID = "previous"
			case "stale-deployment":
				request.Fetch.DeploymentEpoch--
			case "wrong-reader":
				request.Fetch.WorkerID = "old-worker"
			case "wrong-replica":
				request.ReplicaWorkerID = "other"
			case "uncommitted":
				cp.Status = CheckpointInProgress
			case "wrong-snapshot-epoch":
				request.Fetch.EpochID++
			case "not-latest":
				c.jobs["job"].LatestCheckpoint++
			case "wrong-state-path":
				cp.StatePaths["task"] = "other:1"
			}
			for key, value := range map[string]any{string(CheckpointKey("job", 7)): cp, string(JobAssignmentsKey("job")): assignment} {
				data, err := protocol.EncodeMsgPack(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Set([]byte(key), data); err != nil {
					t.Fatal(err)
				}
			}
			data, err := protocol.EncodeMsgPack(request)
			if err != nil {
				t.Fatal(err)
			}
			result, rpcErr := c.HandleAuthorizeCheckpointFetch(context.Background(), 1, data)
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if result.(*rpc.AcknowledgeCheckpointResponse).Accepted != (mode == "valid-old-snapshot") {
				t.Fatalf("authorization: %+v", result)
			}
		})
	}
}

func TestRescaleFetchRequiresAssignedSourceAndTarget(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-source", "wrong-target", "wrong-attempt"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.jobs["job"] = &JobMeta{ID: "job", Status: JobDeploying, LatestCheckpoint: 7}
			c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			cp := CheckpointMeta{ID: 7, JobID: "job", EpochID: 2, Status: CheckpointCompleted, Tasks: map[string]string{"old": "old-worker", "unassigned": "old-worker"}, Replicas: map[string]string{"old": "replica:1", "unassigned": "replica:1"}, StatePaths: map[string]string{"old": "replica:1", "unassigned": "replica:1"}}
			assignment := TaskAssignmentMap{JobID: "job", AttemptID: "current", Assignments: map[string]string{"new": "worker"}, RescaleParts: map[string][]RescaleStatePart{"new": {{SourceTaskID: "old", ReplicaAddress: "replica:1"}}}}
			request := rpc.AuthorizeCheckpointFetchRequest{ReplicaWorkerID: "replica", Fetch: rpc.FetchCheckpointRequest{WorkerID: "worker", AttemptID: "current", DeploymentEpoch: 5, JobID: "job", TaskID: "old", TargetTaskID: "new", CheckpointID: 7, EpochID: 2}}
			switch mode {
			case "wrong-source":
				request.Fetch.TaskID = "unassigned"
			case "wrong-target":
				request.Fetch.TargetTaskID = "other"
			case "wrong-attempt":
				request.Fetch.AttemptID = "previous"
			}
			if err := store.Set(CheckpointKey("job", 7), encode(t, cp)); err != nil {
				t.Fatal(err)
			}
			if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
				t.Fatal(err)
			}
			result, rpcErr := c.HandleAuthorizeCheckpointFetch(context.Background(), 1, encode(t, request))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if result.(*rpc.AcknowledgeCheckpointResponse).Accepted != (mode == "valid") {
				t.Fatalf("authorization=%+v", result)
			}
		})
	}
}
