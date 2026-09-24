package coordinator

import (
	"context"
	"testing"

	"github.com/tarungka/wire/internal/rpc"
)

func TestSavepointFetchRequiresExactSuccessorAssignment(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-job", "wrong-task", "wrong-attempt", "wrong-worker", "wrong-epoch", "missing-pin", "wrong-pin", "wrong-successor", "source-running", "later-source-checkpoint", "deleted-savepoint", "failed-savepoint", "wrong-savepoint", "invalid-checkpoint", "unassigned-source", "wrong-replica", "wrong-archive-epoch", "missing-integrity", "legacy-transfer"} {
		t.Run(mode, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.jobs["old"] = &JobMeta{ID: "old", Status: JobCanceled, LatestCheckpoint: 7, UpgradeSuccessorID: "new"}
			c.jobs["new"] = &JobMeta{ID: "new", Status: JobDeploying, LatestCheckpoint: 7, RestoreSavepoint: &SavepointRestoreReference{JobID: "old", SavepointID: "sp", CheckpointID: 7}}
			c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			cp := CheckpointMeta{ID: 7, JobID: "old", EpochID: 2, SavepointID: "sp", Status: CheckpointCompleted, Tasks: map[string]string{"old-task": "previous"}, Replicas: map[string]string{"old-task": "replica:1"}, StatePaths: map[string]string{"old-task": "replica:1"}}
			sp := SavepointMeta{ID: "sp", JobID: "old", CheckpointID: 7, EpochID: 2, Status: SavepointCompleted}
			desc := rpc.CheckpointRestoreDescriptor{SourceJobID: "old", SourceTaskID: "old-task", CheckpointID: 7, EpochID: 2, ReplicaAddress: "replica:1", ArchiveSHA256: "verified-digest", ArchiveSize: 100}
			request := rpc.AuthorizeCheckpointFetchRequest{ReplicaWorkerID: "replica", Fetch: rpc.FetchCheckpointRequest{TargetJobID: "new", TargetTaskID: "new-task", JobID: "old", TaskID: "old-task", WorkerID: "worker", AttemptID: "current", DeploymentEpoch: c.epoch, CheckpointID: 7, EpochID: 2, RequireArchive: true}}
			switch mode {
			case "wrong-job":
				request.Fetch.TargetJobID = "other"
			case "wrong-task":
				request.Fetch.TargetTaskID = "other"
			case "wrong-attempt":
				request.Fetch.AttemptID = "old-attempt"
			case "wrong-worker":
				request.Fetch.WorkerID = "other"
			case "wrong-epoch":
				request.Fetch.DeploymentEpoch++
			case "missing-pin":
				c.jobs["new"].RestoreSavepoint = nil
			case "wrong-pin":
				c.jobs["new"].RestoreSavepoint.JobID = "other"
			case "wrong-successor":
				c.jobs["old"].UpgradeSuccessorID = "other"
			case "source-running":
				c.jobs["old"].Status = JobRunning
			case "later-source-checkpoint":
				c.jobs["old"].LatestCheckpoint++
			case "failed-savepoint":
				sp.Status = SavepointFailed
			case "wrong-savepoint":
				cp.SavepointID = "different"
			case "invalid-checkpoint":
				cp.InvalidReason = "missing archive"
			case "unassigned-source":
				desc.SourceTaskID = "other"
			case "wrong-replica":
				desc.ReplicaAddress = "other"
			case "wrong-archive-epoch":
				desc.EpochID++
			case "missing-integrity":
				desc.ArchiveSHA256 = ""
			case "legacy-transfer":
				request.Fetch.RequireArchive = false
			}
			assignment := TaskAssignmentMap{JobID: "new", EpochID: c.epoch, AttemptID: "current", Assignments: map[string]string{"new-task": "worker"}, RestoreCheckpoints: map[string]rpc.CheckpointRestoreDescriptor{"new-task": desc}}
			for key, value := range map[string]any{string(CheckpointKey("old", 7)): cp, string(JobAssignmentsKey("new")): assignment} {
				if err := store.Set([]byte(key), encode(t, value)); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "deleted-savepoint" {
				if err := store.Set(SavepointKey("old", "sp"), encode(t, sp)); err != nil {
					t.Fatal(err)
				}
			}
			result, rpcErr := c.HandleAuthorizeCheckpointFetch(context.Background(), 0, encode(t, request))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if result.(*rpc.AcknowledgeCheckpointResponse).Accepted != (mode == "valid") {
				t.Fatalf("authorization %+v", result)
			}
		})
	}
}
