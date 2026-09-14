package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestRescaleJobStopsOldAttemptBeforeDeploying(t *testing.T) {
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
	if _, err := c.RescaleJob("job", "save", 129); err == nil {
		t.Fatal("invalid parallelism accepted")
	}
	if job.Status != JobRunning || job.Parallelism != 4 || string(job.Config) != original {
		t.Fatal("rejected request mutated job")
	}
	result, err := c.RescaleOperators("job", "save", map[string]int{"src": 3, "m": 3, "snk": 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != JobFailing || result.Parallelism != 4 || result.RescaleCheckpoint != 7 {
		t.Fatalf("wrong rescale state: %+v", result)
	}
	c.scheduleTick(context.Background())
	commands := c.DrainCommands("worker")
	if len(commands) != 4 {
		t.Fatalf("cancel commands: %+v", commands)
	}
	for _, command := range commands {
		if command.Type != rpc.CommandTypeCancelTask || command.AttemptID != "old" {
			t.Fatal("old deployment not fenced")
		}
	}
	if job.Status != JobFailing {
		t.Fatal("deployed before old tasks stopped")
	}
	for _, task := range old {
		c.taskStatuses[task.TaskID] = rpc.TaskStatusCanceled
	}
	c.scheduleTick(context.Background())
	commands = c.DrainCommands("worker")
	if len(commands) != 3 || job.Status != JobDeploying {
		t.Fatalf("new deployment: %+v, status %v", commands, job.Status)
	}
	raw, err := store.Get(JobAssignmentsKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	var saved TaskAssignmentMap
	if err := protocol.DecodeMsgPack(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		var desc rpc.TaskDescriptor
		if err := protocol.DecodeMsgPack(command.Data, &desc); err != nil {
			t.Fatal(err)
		}
		if command.Type != rpc.CommandTypeDeployTask || desc.AttemptID == "old" || desc.RestoreRescale == nil || desc.RestoreCheckpoint != nil || len(saved.RescaleParts[desc.TaskID]) == 0 {
			t.Fatalf("missing restore or fetch grant: %+v", desc)
		}
	}
}

func TestGlobalRescalePreservesSourceAndSinkParallelism(t *testing.T) {
	c, store := newTestCoordinator(t)
	graph := linearGraph()
	graph.Operators[0].Parallelism = 1
	graph.Operators[2].Parallelism = 1
	for i := range graph.Edges {
		graph.Edges[i].Shuffle = rpc.ShuffleStrategyHash
	}
	tasks, err := buildPhysicalTasks("job", graph, 4)
	if err != nil {
		t.Fatal(err)
	}
	job := &JobMeta{ID: "job", Status: JobRunning, Parallelism: 4, Config: encode(t, graph), LatestCheckpoint: 7}
	c.jobs[job.ID] = job
	cp := CheckpointMeta{ID: 7, JobID: job.ID, EpochID: 2, Status: CheckpointCompleted, NumKeyGroups: 128, SavepointID: "save", TaskDescriptors: tasks, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
	for _, task := range tasks {
		cp.Tasks[task.TaskID] = "worker"
		cp.Replicas[task.TaskID] = "peer:1"
		cp.StatePaths[task.TaskID] = "peer:1"
	}
	if err := store.Set(CheckpointKey(job.ID, 7), encode(t, cp)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(SavepointKey(job.ID, "save"), encode(t, SavepointMeta{ID: "save", JobID: job.ID, CheckpointID: 7, EpochID: 2, NumKeyGroups: 128, Status: SavepointCompleted})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RescaleJob(job.ID, "save", 8); err != nil {
		t.Fatal(err)
	}
	var updated rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Operators[0].Parallelism != 1 || updated.Operators[1].Parallelism != 8 || updated.Operators[2].Parallelism != 1 {
		t.Fatalf("unexpected topology: %+v", updated.Operators)
	}
	if job.RescaleRollback == nil || string(job.RescaleRollback.Config) != string(encode(t, graph)) {
		t.Fatal("old configuration not retained")
	}
	data, err := store.Get(JobMetaKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var persisted JobMeta
	if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.RescaleRollback == nil {
		t.Fatal("rollback lost across coordinator restart")
	}
}
