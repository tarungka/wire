package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestRestartWaitsForOldTasksAndDeploysCheckpoint(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobFailing, Parallelism: 1, Config: encode(t, linearGraph()), LatestCheckpoint: 7}
	c.jobs["job"] = job
	c.workers["worker"] = &WorkerMeta{ID: "worker", TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
	assignment := TaskAssignmentMap{JobID: "job", EpochID: 5, AttemptID: "old", Assignments: map[string]string{"job/m/0": "worker"}}
	if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	cp := CheckpointMeta{ID: 7, JobID: "job", EpochID: 2, Status: CheckpointCompleted, Tasks: map[string]string{"job/m/0": "worker"}, Replicas: map[string]string{"job/m/0": "replica:1"}, StatePaths: map[string]string{"job/m/0": "replica:1"}}
	if err := store.Set(CheckpointKey("job", 7), encode(t, cp)); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses["job/m/0"] = rpc.TaskStatusRunning
	c.scheduleTick(context.Background())
	commands := c.DrainCommands("worker")
	if len(commands) != 1 || commands[0].Type != rpc.CommandTypeCancelTask || commands[0].AttemptID != "old" || commands[0].EpochID != 5 || job.Status != JobFailing {
		t.Fatalf("old execution not stopped first: %+v", commands)
	}
	c.taskStatuses["job/m/0"] = rpc.TaskStatusCanceled
	c.scheduleTick(context.Background())
	commands = c.DrainCommands("worker")
	if len(commands) != 1 || commands[0].Type != rpc.CommandTypeDeployTask || job.RestartCount != 1 || job.Status != JobDeploying {
		t.Fatalf("restart not deployed: %+v job %+v", commands, job)
	}
	var desc rpc.TaskDescriptor
	if err := protocol.DecodeMsgPack(commands[0].Data, &desc); err != nil {
		t.Fatal(err)
	}
	if desc.AttemptID == "old" || desc.AttemptID == "" || desc.RestoreCheckpoint == nil || desc.RestoreCheckpoint.CheckpointID != 7 {
		t.Fatalf("restart identity: %+v", desc)
	}
	if _, ok := c.taskStatuses["job/m/0"]; ok {
		t.Fatal("old terminal status retained")
	}
}
