package coordinator

import (
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTransactionalCommitDecisionSurvivesCoordinatorReopen(t *testing.T) {
	root := t.TempDir()
	store, err := NewPebbleStore(root)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := newTestCoordinator(t)
	c.store = store
	epoch := make([]byte, 8)
	binary.BigEndian.PutUint64(epoch, c.epoch)
	if err := store.Set(ClusterEpochKey(), epoch); err != nil {
		t.Fatal(err)
	}
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning, DeploymentGeneration: 7}
	tasks := manifestTaskDescriptors("sink")
	tasks[0].OperatorChain[0].Type = rpc.OperatorTypeSink
	assignment := TaskAssignmentMap{JobID: "job", AttemptID: "old", EpochID: c.epoch, TaskDescriptors: tasks, Assignments: map[string]string{"sink": "worker"}, Replicas: map[string]string{"sink": "replica"}}
	if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	state := manifestState(t, "sink", "replica")
	var task engine.TaskMeta
	if err := json.Unmarshal(state.Manifest, &task); err != nil {
		t.Fatal(err)
	}
	task.SinkPrepared = true
	state.Manifest, err = json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AcknowledgeCheckpoint(rpc.AcknowledgeCheckpointRequest{AttemptID: "old", JobID: "job", TaskID: "sink", WorkerID: "worker", CheckpointID: cp.ID, EpochID: cp.EpochID, State: state}); err != nil {
		t.Fatal(err)
	}
	pending, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	// Discard the coordinator and every queued command. Only durable metadata
	// may authorize a Commit after restart, regardless of delivery before failure.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPebbleStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	recovered, err := recoverFromStore(reopened)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.jobs["job"].DeploymentGeneration != 7 || recovered.jobs["job"].LatestCheckpoint != cp.ID {
		t.Fatal("durable writer/commit boundary lost")
	}
	if len(recovered.checkpointsToAbort) != 1 || recovered.checkpointsToAbort[0].ID != pending.ID {
		t.Fatal("completed decision confused with orphan checkpoint")
	}
	replacement, _ := newTestCoordinator(t)
	replacement.store = reopened
	replacement.jobs = recovered.jobs
	replacement.epoch = recovered.epoch
	selected, inventory, err := replacement.selectRecoveryCheckpointLocked(recovered.jobs["job"])
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != cp.ID || !inventory["sink"].SinkPrepared {
		t.Fatal("replacement did not select prepared handle for idempotent Commit")
	}
}
