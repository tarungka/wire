package coordinator

import (
	"strings"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestSavepointRetainsConfiguredKeyGroupCount(t *testing.T) {
	c, _ := newTestCoordinator(t)
	graph := linearGraph()
	graph.NumKeyGroups = 256
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning, Parallelism: 1, Config: encode(t, graph)}
	installSavepointAssignment(t, c, "job")
	sp, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if sp.NumKeyGroups != 256 {
		t.Fatalf("count=%d", sp.NumKeyGroups)
	}
}

func TestCheckpointRestoreRejectsChangedKeyGroupCount(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", LatestCheckpoint: 1}
	cp := CheckpointMeta{ID: 1, JobID: "job", Status: CheckpointCompleted, NumKeyGroups: 256, Tasks: map[string]string{"task": "old"}, StatePaths: map[string]string{"task": "replica"}, Replicas: map[string]string{"task": "replica"}}
	if err := store.Set(CheckpointKey("job", 1), encode(t, cp)); err != nil {
		t.Fatal(err)
	}
	assignments := map[string][]rpc.TaskDescriptor{"worker": {{TaskID: "task", NumKeyGroups: 128}}}
	if err := c.attachCheckpointRestoreLocked(job, assignments); err == nil || !strings.Contains(err.Error(), "count mismatch") {
		t.Fatalf("mismatch=%v", err)
	}
	if assignments["worker"][0].RestoreCheckpoint != nil {
		t.Fatal("invalid restore attached")
	}
	assignments["worker"][0].NumKeyGroups = 256
	if err := c.attachCheckpointRestoreLocked(job, assignments); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRetainsDeploymentTopology(t *testing.T) {
	c, store := newTestCoordinator(t)
	graph := linearGraph()
	tasks, err := buildPhysicalTasks("job", graph, 3)
	if err != nil {
		t.Fatal(err)
	}
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning, Parallelism: 3, Config: encode(t, graph)}
	assignment := TaskAssignmentMap{JobID: "job", TaskDescriptors: tasks, Assignments: map[string]string{}, Replicas: map[string]string{}}
	for _, task := range tasks {
		assignment.Assignments[task.TaskID] = "worker"
		assignment.Replicas[task.TaskID] = "replica"
	}
	if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.Get(CheckpointKey("job", cp.ID))
	if err != nil {
		t.Fatal(err)
	}
	var saved CheckpointMeta
	if err := protocol.DecodeMsgPack(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.TaskDescriptors) != 3 {
		t.Fatalf("topology=%+v", saved.TaskDescriptors)
	}
	for i, task := range saved.TaskDescriptors {
		if task.TaskID != tasks[i].TaskID || task.KeyGroup != tasks[i].KeyGroup || len(task.OperatorChain) != 3 {
			t.Fatalf("task %d changed: %+v", i, task)
		}
	}
}
