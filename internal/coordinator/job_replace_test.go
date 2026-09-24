package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSameLayoutReplacementPreservesFullSnapshotAndRollsBack(t *testing.T) {
	c, store := newTestCoordinator(t)
	graph := linearGraph()
	old, err := buildPhysicalTasks("job", graph, 4)
	if err != nil {
		t.Fatal(err)
	}
	job := &JobMeta{ID: "job", Status: JobRunning, Parallelism: 4, Config: encode(t, graph), LatestCheckpoint: 7}
	c.jobs["job"] = job
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "localhost:1234", TaskSlotsAvailable: 8, LastHeartbeat: time.Now()}
	assignment := TaskAssignmentMap{JobID: "job", EpochID: 5, AttemptID: "old", Assignments: map[string]string{}}
	cp := CheckpointMeta{ID: 7, JobID: "job", EpochID: 2, Status: CheckpointCompleted, NumKeyGroups: 128, SavepointID: "save", TaskDescriptors: old, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
	for _, task := range old {
		assignment.Assignments[task.TaskID] = "worker"
		cp.Tasks[task.TaskID] = "worker"
		cp.Replicas[task.TaskID] = "replica:1"
		cp.StatePaths[task.TaskID] = "replica:1"
		c.taskStatuses[task.TaskID] = rpc.TaskStatusRunning
	}
	sp := SavepointMeta{ID: "save", JobID: "job", CheckpointID: 7, EpochID: 2, NumKeyGroups: 128, Status: SavepointCompleted}
	for _, entry := range []struct {
		key   []byte
		value any
	}{{JobAssignmentsKey("job"), assignment}, {CheckpointKey("job", 7), cp}, {SavepointKey("job", "save"), sp}} {
		if err := store.Set(entry.key, encode(t, entry.value)); err != nil {
			t.Fatal(err)
		}
	}

	original := string(job.Config)
	candidate := linearGraph()
	candidate.Operators[1].ClassName = "new-map"
	candidate.CheckpointPolicy = &rpc.CheckpointPolicy{Interval: time.Second, Timeout: time.Minute}
	result, err := c.replaceJobFromSavepoint("job", "save", 4, encode(t, candidate), "request-123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != JobFailing || result.ReplacementCheckpoint != 7 || result.RescaleCheckpoint != 0 || result.RescaleRollback == nil {
		t.Fatalf("replacement=%+v", result)
	}
	assertRequestPersisted := func() {
		t.Helper()
		data, err := store.Get(JobMetaKey("job"))
		if err != nil {
			t.Fatal(err)
		}
		var persisted JobMeta
		if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
			t.Fatal(err)
		}
		if persisted.ReplacementRequestID != "request-123" || jobDetailFromMeta(&persisted).ReplacementRequestID != "request-123" {
			t.Fatal("accepted request identity lost in storage or API")
		}
	}
	assertRequestPersisted()
	tasks, err := generateTaskDescriptors(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.attachCheckpointRestoreLocked(job, map[string][]rpc.TaskDescriptor{"worker": tasks}); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.RestoreCheckpoint == nil || task.RestoreRescale != nil {
			t.Fatal("replacement did not preserve full task snapshot")
		}
	}
	job.RescaleRollback.Attempted = true
	if err := c.rollbackFailedRescale(job); err != nil {
		t.Fatal(err)
	}
	assertRequestPersisted()
	if string(job.Config) != original || job.CheckpointPolicy != nil || job.ReplacementCheckpoint != 0 || job.RescaleRollback != nil {
		t.Fatal("rollback did not restore graph and policy")
	}
}
