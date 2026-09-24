package coordinator

import (
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func checkpointPolicyCoordinator(t *testing.T) (*Coordinator, *MemoryStore) {
	t.Helper()
	c, store := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobRunning}
	assignment := TaskAssignmentMap{TaskDescriptors: manifestTaskDescriptors("task"), JobID: "job", Assignments: map[string]string{"task": "worker"}, Replicas: map[string]string{"task": "replica"}}
	if err := store.Set(JobAssignmentsKey("job"), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	return c, store
}

func TestCheckpointTimeoutPolicyAndDuplicateAbort(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	c.config.CheckpointMaxConsecutiveFailures = 3
	for i := 1; i <= 3; i++ {
		cp, err := c.TriggerCheckpoint("job")
		if err != nil {
			t.Fatal(err)
		}
		c.DrainCommands("worker")
		c.expireCheckpoints(cp.Timestamp.Add(c.config.CheckpointTimeout))
		if err := c.AbortCheckpoint("job", cp.ID, cp.EpochID); err != nil {
			t.Fatal(err)
		}
		if c.jobs["job"].CheckpointFailures != uint64(i) {
			t.Fatal("duplicate abort charged twice")
		}
		commands := c.DrainCommands("worker")
		if len(commands) != 2 || commands[0].Type != rpc.CommandTypeAbortCheckpoint {
			t.Fatal("timeout did not send abort")
		}
		want := JobRunning
		if i == 3 {
			want = JobFailing
		}
		if c.jobs["job"].Status != want {
			t.Fatalf("status after %d failures: %v", i, c.jobs["job"].Status)
		}
	}
	raw, err := store.Get(JobMetaKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	var restored JobMeta
	if err := protocol.DecodeMsgPack(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status != JobFailing || restored.CheckpointFailures != 3 || restored.ConsecutiveCheckpointFailures != 3 {
		t.Fatalf("policy was not persisted: %+v", restored)
	}
}

func TestCheckpointSuccessResetsConsecutiveFailuresAndEnforcesMinPause(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	first, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	c.expireCheckpoints(first.Timestamp.Add(c.config.CheckpointTimeout))
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	ack := rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: cp.ID, EpochID: cp.EpochID, State: manifestState(t, "task", "replica")}
	if err := c.AcknowledgeCheckpoint(ack); err != nil {
		t.Fatal(err)
	}
	completion := c.jobs["job"].LastCheckpointCompletion
	if c.jobs["job"].ConsecutiveCheckpointFailures != 0 || completion.IsZero() {
		t.Fatal("success did not reset policy")
	}
	c.config.CheckpointMinPause = time.Minute
	if _, err := c.TriggerCheckpoint("job"); !errors.Is(err, ErrCheckpointMinPause) {
		t.Fatalf("minimum pause: %v", err)
	}
	if err := c.AcknowledgeCheckpoint(ack); err != nil {
		t.Fatal(err)
	}
	if !c.jobs["job"].LastCheckpointCompletion.Equal(completion) {
		t.Fatal("duplicate ACK reset completion clock")
	}
	c.jobs["job"].LastCheckpointCompletion = time.Now().Add(-2 * time.Minute)
	if _, err := c.TriggerCheckpoint("job"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointFailureRateAndUnlimitedDefault(t *testing.T) {
	for _, rate := range []float64{0, 0.5} {
		c, _ := checkpointPolicyCoordinator(t)
		c.config.CheckpointTolerableFailureRate = rate
		cp, err := c.TriggerCheckpoint("job")
		if err != nil {
			t.Fatal(err)
		}
		req := rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: cp.ID, EpochID: cp.EpochID, Failure: "upload failed"}
		if err := c.ReportCheckpointFailure(req); err != nil {
			t.Fatal(err)
		}
		want := JobRunning

		if c.jobs["job"].Status != want {
			t.Fatalf("rate %f: %v", rate, c.jobs["job"].Status)
		}
	}
}

func TestSavepointBypassesCheckpointMinPause(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.config.CheckpointMinPause = time.Hour
	c.jobs["job"].LastCheckpointCompletion = time.Now()
	if _, err := c.TriggerCheckpoint("job"); !errors.Is(err, ErrCheckpointMinPause) {
		t.Fatalf("ordinary checkpoint should be paced: %v", err)
	}
	sp, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Status != SavepointInProgress {
		t.Fatalf("savepoint status: %v", sp.Status)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil || !queued.Queued || queued.CheckpointID != 0 || c.activeCheckpoints["job"].ID != sp.CheckpointID {
		t.Fatalf("queued savepoint must not overlap active checkpoint: %+v %v", queued, err)
	}
}

func TestCheckpointPolicyMetadataCompatibility(t *testing.T) {
	// Model the subset understood by a coordinator before the policy fields.
	type legacyJob struct {
		ID     string    `codec:"id"`
		Status JobStatus `codec:"status"`
	}
	current := JobMeta{ID: "job", Status: JobRunning, CheckpointAttempts: 10, CheckpointFailures: 2, ConsecutiveCheckpointFailures: 1, LastCheckpointCompletion: time.Now(), CheckpointFailure: "timeout"}
	var old legacyJob
	if err := protocol.DecodeMsgPack(encode(t, current), &old); err != nil {
		t.Fatal(err)
	}
	if old.ID != current.ID || old.Status != current.Status {
		t.Fatal("legacy decoder lost existing fields")
	}
	var restored JobMeta
	if err := protocol.DecodeMsgPack(encode(t, old), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ID != current.ID || restored.CheckpointAttempts != 0 || restored.CheckpointFailures != 0 || restored.ConsecutiveCheckpointFailures != 0 || !restored.LastCheckpointCompletion.IsZero() || restored.CheckpointFailure != "" {
		t.Fatalf("old metadata must load with default policy history: %+v", restored)
	}
}

func TestCheckpointPolicyRecoveryAndSavepointIsolation(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	c.config.CheckpointMaxConsecutiveFailures = 2
	fail := func(cp *CheckpointMeta) {
		t.Helper()
		err := c.ReportCheckpointFailure(rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: cp.ID, EpochID: cp.EpochID, Failure: "upload failed"})
		if err != nil {
			t.Fatal(err)
		}
		c.DrainCommands("worker")
	}
	cp, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	fail(cp)
	sp, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	c.expireCheckpoints(time.Now().Add(c.config.CheckpointTimeout + time.Second))
	job := c.jobs["job"]
	if job.Status != JobRunning || job.CheckpointAttempts != 1 || job.CheckpointFailures != 1 || job.ConsecutiveCheckpointFailures != 1 || len(job.CheckpointOutcomes) != 1 {
		t.Fatalf("savepoint %s charged policy: %+v", sp.ID, job)
	}
	cp, err = c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	fail(cp)
	if job.Status != JobFailing {
		t.Fatal("consecutive limit not enforced")
	}
	if err := c.transitionJob(job, JobDeploying); err != nil {
		t.Fatal(err)
	}
	if err := c.transitionJob(job, JobRunning); err != nil {
		t.Fatal(err)
	}
	cp, err = c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	fail(cp)
	if job.Status != JobRunning || job.ConsecutiveCheckpointFailures != 1 || len(job.CheckpointOutcomes) != 1 {
		t.Fatalf("recovery retained policy history: %+v", job)
	}
}

func TestCheckpointRateUsesBoundedCompletedOutcomes(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	c.config.CheckpointTolerableFailureRate = .01
	for i := 0; i < 200; i++ {
		cp, err := c.TriggerCheckpoint("job")
		if err != nil {
			t.Fatal(err)
		}
		req := rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: cp.ID, EpochID: cp.EpochID, State: manifestState(t, "task", "replica")}
		if i >= 198 {
			req.Failure = "upload failed"
			err = c.ReportCheckpointFailure(req)
		} else {
			err = c.AcknowledgeCheckpoint(req)
		}
		if err != nil {
			t.Fatal(err)
		}
		c.DrainCommands("worker")
		if i < 199 && c.jobs["job"].Status != JobRunning {
			t.Fatalf("premature failure at %d", i)
		}
	}
	if c.jobs["job"].Status != JobFailing {
		t.Fatal("recent failures hidden by successful history")
	}
	raw, err := store.Get(JobMetaKey("job"))
	if err != nil {
		t.Fatal(err)
	}
	var restored JobMeta
	if err := protocol.DecodeMsgPack(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.CheckpointOutcomes) != 100 {
		t.Fatal("bounded window not persisted")
	}
}

func TestSavepointOutcomesDoNotChangeCheckpointBudget(t *testing.T) {
	for _, failed := range []bool{false, true} {
		c, _ := checkpointPolicyCoordinator(t)
		c.config.CheckpointMaxConsecutiveFailures = 1
		job := c.jobs["job"]
		job.ConsecutiveCheckpointFailures = 1
		job.CheckpointOutcomes = []bool{true}
		sp, err := c.TriggerSavepoint("job")
		if err != nil {
			t.Fatal(err)
		}
		req := rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: sp.CheckpointID, EpochID: sp.EpochID, State: manifestState(t, "task", "replica")}
		if failed {
			req.Failure = "upload failed"
			err = c.ReportCheckpointFailure(req)
		} else {
			err = c.AcknowledgeCheckpoint(req)
		}
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != JobRunning || job.CheckpointAttempts != 0 || job.CheckpointFailures != 0 || job.ConsecutiveCheckpointFailures != 1 || len(job.CheckpointOutcomes) != 1 {
			t.Fatalf("savepoint modified policy: %+v", job)
		}
	}
}
