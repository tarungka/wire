package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestFinalCheckpointWaitsForAllSourcesAndBypassesMinPause(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.config.CheckpointMinPause = time.Hour
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning, LastCheckpointCompletion: time.Now()}
	assignment := TaskAssignmentMap{JobID: "job", AttemptID: "attempt", Assignments: map[string]string{"one": "worker", "two": "worker"}, Replicas: map[string]string{"one": "replica", "two": "replica"}}
	for _, id := range []string{"one", "two"} {
		assignment.TaskDescriptors = append(assignment.TaskDescriptors, rpc.TaskDescriptor{TaskID: id, OperatorChain: []rpc.OperatorDescriptor{{Type: rpc.OperatorTypeSource}}})
	}
	if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses["one"] = rpc.TaskStatusFinishing
	c.taskStatuses["two"] = rpc.TaskStatusRunning
	if _, err := c.triggerCheckpointBoundary("job", "", true); !errors.Is(err, errFinalCheckpointNotReady) {
		t.Fatal(err)
	}
	c.taskStatuses["two"] = rpc.TaskStatusFinishing
	c.scheduleFinalCheckpoints(t.Context())
	cp, ok := c.activeCheckpoints["job"]
	if !ok || !cp.Final {
		t.Fatalf("final boundary not allocated: %+v", cp)
	}
	commands := c.DrainCommands("worker")
	if len(commands) != 2 {
		t.Fatal(commands)
	}
	for _, command := range commands {
		var request rpc.TriggerCheckpointRequest
		if err := protocol.DecodeMsgPack(command.Data, &request); err != nil {
			t.Fatal(err)
		}
		if !request.Final || request.AttemptID != "attempt" {
			t.Fatal(request)
		}
	}
	// Unlimited ordinary checkpoint failure tolerance cannot strand an EOF job.
	if err := c.AbortCheckpoint("job", cp.ID, cp.EpochID); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].Status != JobFailing {
		t.Fatal("aborted final checkpoint did not request replay")
	}
}
