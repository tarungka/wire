package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func removalJob(t *testing.T) (*Coordinator, *MemoryStore, *JobMeta) {
	t.Helper()
	c, store, job := cancellationJob(t, JobRunning)
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{"task": "worker"}}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	if err := c.persistWorker(&WorkerMeta{ID: "worker", LastHeartbeat: time.Now(), TaskSlotsTotal: 2, TaskSlotsAvailable: 1}); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses["task"] = rpc.TaskStatusRunning
	return c, store, job
}

func TestRemovedWorkerWaitsForTeardownOrLease(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "lease", true: "report"}[terminal], func(t *testing.T) {
			c, _, job := removalJob(t)
			if err := c.RemoveWorker("worker"); err != nil {
				t.Fatal(err)
			}
			c.detectLostTaskWorkers()
			if job.Status != JobFailing || c.taskStatuses["task"] != rpc.TaskStatusRunning {
				t.Fatal("removal must fail job without claiming task stopped")
			}
			if c.prepareTaskRestart(job) {
				t.Fatal("replacement permitted while old task can still execute")
			}
			commands := c.DrainCommands("worker")
			if len(commands) != 1 || commands[0].Type != rpc.CommandTypeCancelTask || commands[0].AttemptID != "old" {
				t.Fatalf("missing fenced cancellation: %+v", commands)
			}
			if terminal {
				c.taskStatuses["task"] = rpc.TaskStatusCanceled
			} else {
				c.workers["worker"].LastHeartbeat = time.Now().Add(-2 * c.config.WorkerTimeout)
				c.detectLostTaskWorkers()
			}
			if !c.prepareTaskRestart(job) {
				t.Fatal("stopped task blocked recovery")
			}
		})
	}
}

func TestRemovedWorkerCannotRenewOrReceivePlacement(t *testing.T) {
	c, _, _ := removalJob(t)
	if err := c.RemoveWorker("worker"); err != nil {
		t.Fatal(err)
	}
	contact := c.workers["worker"].LastHeartbeat
	response, rpcErr := c.HandleHeartbeat(context.Background(), 0, encode(t, rpc.HeartbeatRequest{WorkerID: "worker", EpochID: c.epoch}))
	if rpcErr != nil || response.(*rpc.HeartbeatResponse).Accepted || !c.workers["worker"].LastHeartbeat.Equal(contact) {
		t.Fatal("removed worker renewed execution authority")
	}
	if _, err := c.RegisterWorker(RegisterWorkerRequest{WorkerID: "worker", TaskSlotsTotal: 10}); err == nil {
		t.Fatal("removed identity silently rejoined")
	}
	// Late terminal reports may change accounting, but never restore admission.
	c.workers["worker"].TaskSlotsAvailable = 10
	if _, err := c.assignTasks([]rpc.TaskDescriptor{{TaskID: "replacement"}}); err == nil {
		t.Fatal("placed on removed worker")
	}
	if c.assignmentsLiveLocked(map[string][]rpc.TaskDescriptor{"worker": {{TaskID: "replacement"}}}, time.Now()) {
		t.Fatal("accepted stale placement plan")
	}
	if c.aliveWorkerCount() != 0 {
		t.Fatal("removed worker counted alive")
	}
}

func TestWorkerRemovalPersistenceFailureIsAtomic(t *testing.T) {
	c, store, job := removalJob(t)
	fault := &workerLossStore{MetadataStore: store, failKey: WorkerMetaKey("worker")}
	c.store = fault
	if err := c.RemoveWorker("worker"); err == nil {
		t.Fatal("expected store failure")
	}
	worker := c.workers["worker"]
	if worker.Removed || worker.TaskSlotsAvailable != 1 || job.Status != JobRunning {
		t.Fatal("failed write changed live admission")
	}
	fault.failKey = nil
	if err := c.RemoveWorker("worker"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveWorker("worker"); err != nil {
		t.Fatal("removal is not idempotent", err)
	}
}

func TestWorkerRemovalSurvivesRecoveryAndHonorsOldEpochFence(t *testing.T) {
	c, _, job := removalJob(t)
	if err := c.RemoveWorker("worker"); err != nil {
		t.Fatal(err)
	}
	c.detectLostTaskWorkers()
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	if !c.workers["worker"].Removed {
		t.Fatal("removal was not durable")
	}
	c.detectLostTaskWorkers()
	job = c.jobs[job.ID]
	if c.prepareTaskRestart(job) {
		t.Fatal("recovery bypassed prior execution lease")
	}
	c.recoveryFenceUntil = time.Now().Add(-time.Second)
	if !c.prepareTaskRestart(job) {
		t.Fatal("expired recovery fence still blocks restart")
	}
}
