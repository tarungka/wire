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
	assignment := TaskAssignmentMap{JobID: "job", Assignments: map[string]string{"task": "worker"}, Replicas: map[string]string{"task": "replica"}}
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
	ack := rpc.AcknowledgeCheckpointRequest{WorkerID: "worker", JobID: "job", TaskID: "task", CheckpointID: cp.ID, EpochID: cp.EpochID, State: &rpc.StateHandle{TaskID: "task", Path: "replica"}}
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
		if rate > 0 {
			want = JobFailing
		}
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
	if _, err := c.TriggerSavepoint("job"); !errors.Is(err, ErrCheckpointInProgress) {
		t.Fatalf("savepoints must still enforce one in-flight checkpoint: %v", err)
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
