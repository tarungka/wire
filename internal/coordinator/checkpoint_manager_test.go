package coordinator

import (
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestTriggerCheckpointPersistsBoundaryAndRejectsOverlap(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	data, err := protocol.EncodeMsgPack(TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey("job"), data); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ID != 1 || checkpoint.EpochID != 5 || checkpoint.Tasks["task"] != "worker" {
		t.Fatalf("boundary: %+v", checkpoint)
	}
	persisted, err := store.Get(CheckpointKey("job", 1))
	if err != nil {
		t.Fatal(err)
	}
	var decoded CheckpointMeta
	if err := protocol.DecodeMsgPack(persisted, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Status != CheckpointInProgress || decoded.EpochID != 5 {
		t.Fatalf("persisted: %+v", decoded)
	}
	if _, err := c.TriggerCheckpoint("job"); err == nil {
		t.Fatal("overlapping checkpoint accepted")
	}
	if err := c.AbortCheckpoint("job", 1, 4); err == nil {
		t.Fatal("stale epoch aborted checkpoint")
	}
	failure := rpc.AcknowledgeCheckpointRequest{JobID: "job", TaskID: "task", WorkerID: "wrong", CheckpointID: 1, EpochID: 5, Failure: "replica unavailable"}
	if err := c.ReportCheckpointFailure(failure); err == nil {
		t.Fatal("unassigned worker aborted checkpoint")
	}
	failure.WorkerID = "worker"
	for range 2 {
		if err := c.ReportCheckpointFailure(failure); err != nil {
			t.Fatal(err)
		}
	}
	next, err := c.TriggerCheckpoint("job")
	if err != nil || next.ID != 2 {
		t.Fatalf("checkpoint after abort: %+v, %v", next, err)
	}
}

func TestCheckpointCompletesOnlyAfterEveryAssignedTask(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	data, err := protocol.EncodeMsgPack(TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"a": "w1", "b": "w2"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(JobAssignmentsKey("job"), data); err != nil {
		t.Fatal(err)
	}
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	c.DrainCommands("w1")
	c.DrainCommands("w2")
	req := rpc.AcknowledgeCheckpointRequest{JobID: "job", TaskID: "a", WorkerID: "wrong", CheckpointID: cp.ID, EpochID: 5, State: &rpc.StateHandle{TaskID: "a", Path: "replica/a"}}
	if err := c.AcknowledgeCheckpoint(req); err == nil {
		t.Fatal("wrong worker accepted")
	}
	req.WorkerID = "w1"
	if err := c.AcknowledgeCheckpoint(req); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].LatestCheckpoint != 0 {
		t.Fatal("partial checkpoint committed")
	}
	if len(c.DrainCommands("w1")) != 0 || len(c.DrainCommands("w2")) != 0 {
		t.Fatal("commit notification preceded all acknowledgements")
	}
	req.TaskID, req.WorkerID, req.State = "b", "w2", &rpc.StateHandle{TaskID: "b", Path: "replica/b"}
	if err := c.AcknowledgeCheckpoint(req); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].LatestCheckpoint != cp.ID {
		t.Fatal("complete checkpoint not committed")
	}
	for _, worker := range []string{"w1", "w2"} {
		commands := c.DrainCommands(worker)
		if len(commands) != 1 || commands[0].Type != rpc.CommandTypeCommitCheckpoint {
			t.Fatalf("commit notification for %s: %+v", worker, commands)
		}
	}
	if err := c.AcknowledgeCheckpoint(req); err != nil {
		t.Fatal(err)
	}
	req.State.Path = "different"
	if err := c.AcknowledgeCheckpoint(req); err == nil {
		t.Fatal("conflicting retry accepted")
	}
}
