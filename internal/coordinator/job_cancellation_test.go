package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func cancellationJob(t *testing.T, status JobStatus) (*Coordinator, *MemoryStore, *JobMeta) {
	t.Helper()
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Name: "cancel-me", Status: status, UpdatedAt: time.Now().UTC()}
	if err := c.persistJob(job); err != nil {
		t.Fatal(err)
	}
	c.activeJobNames[job.Name] = job.ID
	return c, store, job
}

func TestCancellationRetriesUntilEveryTaskStops(t *testing.T) {
	c, store, job := cancellationJob(t, JobRunning)
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "attempt", Assignments: map[string]string{"task": "worker"}}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: time.Now(), TaskSlotsTotal: 1, RunningTasks: []string{"task"}}
	c.taskStatuses["task"] = rpc.TaskStatusRunning
	snapshot, err := c.CancelJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		c.scheduleTick(context.Background())
		if job.Status != JobCanceling {
			t.Fatal("cancellation finished before teardown acknowledgement")
		}
		cmds := c.DrainCommands("worker")
		if len(cmds) != 1 || cmds[0].Type != rpc.CommandTypeCancelTask || cmds[0].AttemptID != "attempt" || cmds[0].EpochID != c.epoch {
			t.Fatalf("commands = %+v", cmds)
		}
	}
	response, rpcErr := c.HandleUpdateTaskStatus(context.Background(), 1, encode(t, rpc.UpdateTaskStatusRequest{JobID: job.ID, WorkerID: "worker", TaskID: "task", AttemptID: "attempt", EpochID: c.epoch, Status: rpc.TaskStatusCanceled}))
	if rpcErr != nil || !response.(*rpc.UpdateTaskStatusResponse).Accepted {
		t.Fatalf("status: %+v %v", response, rpcErr)
	}
	c.scheduleTick(context.Background())
	if job.Status != JobCanceled || snapshot.Status != JobCanceling {
		t.Fatal("cancellation did not complete or mutated its API snapshot")
	}
	if c.workers["worker"].TaskSlotsAvailable != 1 {
		t.Fatal("cancelled task did not release its slot")
	}
	data, _ := store.Get(JobMetaKey(job.ID))
	var persisted JobMeta
	if err := protocol.DecodeMsgPack(data, &persisted); err != nil || persisted.Status != JobCanceled {
		t.Fatalf("terminal decision not durable: %+v %v", persisted, err)
	}
	if _, ok := c.activeJobNames[job.Name]; ok {
		t.Fatal("cancelled job retains its name reservation")
	}
}

func TestCancellationWithoutAssignmentsAndPausedCancellation(t *testing.T) {
	for _, status := range []JobStatus{JobCreated, JobPaused, JobFailing, JobFinishing} {
		t.Run(status.String(), func(t *testing.T) {
			c, _, job := cancellationJob(t, status)
			if _, err := c.CancelJob(job.ID); err != nil {
				t.Fatal(err)
			}
			c.scheduleTick(context.Background())
			if job.Status != JobCanceled {
				t.Fatalf("status = %s", job.Status)
			}
		})
	}
}

func TestCancellationRetriesFailedTerminalWrite(t *testing.T) {
	c, store, job := cancellationJob(t, JobRunning)
	if _, err := c.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	fault := &workerLossStore{MetadataStore: store, failKey: JobMetaKey(job.ID)}
	c.store = fault
	if err := c.advanceCancellation(job.ID, time.Now()); err == nil {
		t.Fatal("expected persistence failure")
	}
	if job.Status != JobCanceling || c.activeJobNames[job.Name] != job.ID {
		t.Fatal("undurable cancellation published")
	}
	fault.failKey = nil
	if err := c.advanceCancellation(job.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCanceled {
		t.Fatal("terminal write was not retried")
	}
}

func TestCancellationWaitsForLostWorkerAuthority(t *testing.T) {
	c, store, job := cancellationJob(t, JobRunning)
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{"task": "deleted-worker"}}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceCancellation(job.ID, job.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCanceling {
		t.Fatal("deleted metadata was mistaken for task shutdown")
	}
	if err := c.advanceCancellation(job.ID, job.UpdatedAt.Add(c.config.WorkerTimeout)); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCanceled {
		t.Fatal("expired authority still blocks cancellation")
	}
}

func TestCancellationAfterRecoveryWaitsForOldEpochFence(t *testing.T) {
	c, store, job := cancellationJob(t, JobCanceling)
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{"task": "worker"}}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	if err := c.persistWorker(&WorkerMeta{ID: "worker", LastHeartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct jobs and workers through production recovery, which forgets
	// fresh contacts and installs the old epoch's execution-authority fence.
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	job = c.jobs[job.ID]
	now := time.Now()
	if err := c.advanceCancellation(job.ID, now); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCanceling {
		t.Fatal("recovered cancellation ignored old worker lease")
	}
	if err := c.advanceCancellation(job.ID, c.recoveryFenceUntil); err != nil {
		t.Fatal(err)
	}
	if job.Status != JobCanceled {
		t.Fatal("recovered cancellation did not finish after fencing")
	}
}

func TestCancellationPersistsAbortBeforeStoppingTasks(t *testing.T) {
	c, store, job := cancellationJob(t, JobRunning)
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "attempt", Assignments: map[string]string{"task": "worker"}, Replicas: map[string]string{"task": "replica"}, TaskDescriptors: manifestTaskDescriptors("task")}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: time.Now()}
	checkpoint, err := c.TriggerCheckpoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.DrainCommands("worker")
	if _, err := c.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	fault := &deploymentBatchStore{MetadataStore: store, fail: true}
	c.store = fault
	if err := c.advanceCancellation(job.ID, time.Now()); err == nil {
		t.Fatal("expected failed abort persistence")
	}
	if len(c.DrainCommands("worker")) != 0 || job.Status != JobCanceling {
		t.Fatal("stopped tasks before checkpoint decision became durable")
	}
	fault.fail = false
	if err := c.advanceCancellation(job.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	cmds := c.DrainCommands("worker")
	if len(cmds) != 2 || cmds[0].Type != rpc.CommandTypeAbortCheckpoint || cmds[1].Type != rpc.CommandTypeCancelTask {
		t.Fatalf("abort/cancel ordering: %+v", cmds)
	}
	raw, err := store.Get(CheckpointKey(job.ID, checkpoint.ID))
	if err != nil {
		t.Fatal(err)
	}
	var persisted CheckpointMeta
	if err := protocol.DecodeMsgPack(raw, &persisted); err != nil || persisted.Status != CheckpointAborted {
		t.Fatalf("checkpoint not aborted: %+v %v", persisted, err)
	}
	if len(c.activeCheckpoints) != 0 || job.CheckpointFailures != 0 {
		t.Fatal("user cancellation retained snapshot or charged failure budget")
	}
}
