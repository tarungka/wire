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

func TestRestartDoesNotWaitForExpiredWorker(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobRunning, LatestCheckpoint: 7}
	c.jobs[job.ID] = job
	c.workers["dead"] = &WorkerMeta{LastHeartbeat: time.Now().Add(-2 * c.config.WorkerTimeout)}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID, Assignments: map[string]string{"task": "dead"}})); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses["task"] = rpc.TaskStatusRunning
	c.detectLostTaskWorkers()
	if job.Status != JobFailing {
		t.Fatal("worker loss did not fail running job")
	}
	if !c.prepareTaskRestart(job) {
		t.Fatal("expired worker prevented recovery")
	}
	if len(c.DrainCommands("dead")) != 0 {
		t.Fatal("cancellation queued for dead worker")
	}
}

func TestRestartBudgetAndBackoff(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobFailing, LatestCheckpoint: 7, RecoveryAttempts: 1, UpdatedAt: time.Now()}
	c.jobs[job.ID] = job
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID})); err != nil {
		t.Fatal(err)
	}
	if c.prepareTaskRestart(job) {
		t.Fatal("restart skipped backoff")
	}
	job.UpdatedAt = time.Now().Add(-2 * c.config.RestartBackoff)
	if !c.prepareTaskRestart(job) {
		t.Fatal("elapsed backoff prevented recovery")
	}
	job.RecoveryAttempts = c.config.RestartMaxAttempts
	if c.prepareTaskRestart(job) || job.Status != JobFailed {
		t.Fatal("exhausted restart budget not terminal")
	}
}

func TestRescaleDoesNotConsumeOrBypassFailureBudget(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobFailing, LatestCheckpoint: 7, RescaleCheckpoint: 7, RescaleRequested: true, RestartCount: 99, RecoveryAttempts: c.config.RestartMaxAttempts, UpdatedAt: time.Now()}
	c.jobs[job.ID] = job
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if !c.prepareTaskRestart(job) {
			t.Fatal("rescale blocked by recovery limit/backoff")
		}
		if err := c.transitionJob(job, JobDeploying); err != nil {
			t.Fatal(err)
		}
		if job.RescaleRequested || job.RestartCount != 99 || job.RecoveryAttempts != c.config.RestartMaxAttempts {
			t.Fatalf("rescale consumed budget: %+v", job)
		}
		if err := c.transitionJob(job, JobRunning); err != nil {
			t.Fatal(err)
		}
		if err := c.transitionJob(job, JobFailing); err != nil {
			t.Fatal(err)
		}
		job.RescaleRequested = true
	}
	job.RescaleRequested = false
	if c.prepareTaskRestart(job) || job.Status != JobFailed {
		t.Fatal("failed rescale deployment bypassed budget via retained savepoint")
	}
}

func TestStableRunningResetsRecoveryBudgetAndPersists(t *testing.T) {
	for _, stable := range []bool{false, true} {
		c, store := newTestCoordinator(t)
		job := &JobMeta{ID: "job", Status: JobRunning, RestartCount: 99, RecoveryAttempts: 3, RunningSince: time.Now()}
		if stable {
			job.RunningSince = time.Now().Add(-2 * c.config.RestartResetAfter)
		}
		c.jobs[job.ID] = job
		if err := c.transitionJob(job, JobFailing); err != nil {
			t.Fatal(err)
		}
		want := 3
		if stable {
			want = 0
		}
		if job.RecoveryAttempts != want || job.RestartCount != 99 {
			t.Fatalf("budget=%d lifetime=%d", job.RecoveryAttempts, job.RestartCount)
		}
		data, err := store.Get(JobMetaKey(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		var saved JobMeta
		if err := protocol.DecodeMsgPack(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.RecoveryAttempts != want {
			t.Fatal("budget reset not persisted")
		}
	}
}

func TestLegacyLifetimeRestartsDoNotExhaustNewBudget(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := &JobMeta{ID: "job", Status: JobFailing, LatestCheckpoint: 7, RestartCount: 100}
	c.jobs[job.ID] = job
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID})); err != nil {
		t.Fatal(err)
	}
	if !c.prepareTaskRestart(job) {
		t.Fatal("legacy lifetime count consumed recovery budget")
	}
}

func TestWorkerReregistrationResetsOnlyStableRecoveryBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status JobStatus
		age    time.Duration
		reset  bool
	}{
		{"stable", JobRunning, 2 * time.Minute, true},
		{"recent", JobRunning, time.Second, false},
		{"deploying", JobDeploying, 2 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, store := newTestCoordinator(t)
			job := &JobMeta{ID: "job", Status: tc.status, LatestCheckpoint: 7, RecoveryAttempts: c.config.RestartMaxAttempts, RestartCount: 12, RunningSince: time.Now().Add(-tc.age)}
			c.jobs[job.ID] = job
			assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{"task": "worker"}}
			if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
				t.Fatal(err)
			}
			c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: time.Now()}
			c.taskStatuses["task"] = rpc.TaskStatusRunning
			response, err := c.RegisterWorker(RegisterWorkerRequest{WorkerID: "worker", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.MissingTasks) != 1 || job.Status != JobFailing {
				t.Fatalf("missing task not reconciled: %+v", response)
			}
			want := c.config.RestartMaxAttempts
			if tc.reset {
				want = 0
			}
			if job.RecoveryAttempts != want || job.RestartCount != 12 {
				t.Fatalf("attempts=%d want=%d lifetime=%d", job.RecoveryAttempts, want, job.RestartCount)
			}
			data, err := store.Get(JobMetaKey(job.ID))
			if err != nil {
				t.Fatal(err)
			}
			var persisted JobMeta
			if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if persisted.RecoveryAttempts != want {
				t.Fatal("reset not persisted")
			}
			if ready := c.prepareTaskRestart(job); ready != tc.reset {
				t.Fatalf("restart ready=%v want=%v", ready, tc.reset)
			}
		})
	}
}
