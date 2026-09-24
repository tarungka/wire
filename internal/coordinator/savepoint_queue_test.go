package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func completeQueueCheckpoint(t *testing.T, c *Coordinator, cp CheckpointMeta) {
	t.Helper()
	if err := c.AcknowledgeCheckpoint(rpc.AcknowledgeCheckpointRequest{JobID: "job", TaskID: "task", WorkerID: "worker", CheckpointID: cp.ID, EpochID: cp.EpochID, State: manifestState(t, "task", "replica")}); err != nil {
		t.Fatal(err)
	}
}

func TestSavepointsQueueInOrderWithoutOverlappingCheckpoints(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	current, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Queued || !second.Queued || first.CheckpointID != 0 || second.CheckpointID != 0 {
		t.Fatal("queued requests started a concurrent boundary")
	}
	if err := c.advanceQueuedSavepoint("job"); !errors.Is(err, ErrCheckpointInProgress) {
		t.Fatalf("active checkpoint was bypassed: %v", err)
	}
	completeQueueCheckpoint(t, c, *current)
	if _, err := c.TriggerCheckpoint("job"); !errors.Is(err, ErrCheckpointInProgress) {
		t.Fatalf("periodic checkpoint overtook queue: %v", err)
	}
	for _, queued := range []*SavepointMeta{first, second} {
		if err := c.advanceQueuedSavepoint("job"); err != nil {
			t.Fatal(err)
		}
		active := c.activeCheckpoints["job"]
		if active.SavepointID != queued.ID {
			t.Fatalf("out of order: got %s want %s", active.SavepointID, queued.ID)
		}
		sp, err := c.GetSavepoint("job", queued.ID)
		if err != nil || sp.Queued || sp.CheckpointID != active.ID || !sp.TriggerTime.Equal(queued.TriggerTime) {
			t.Fatalf("activation lost request identity: %+v %v", sp, err)
		}
		completeQueueCheckpoint(t, c, active)
		sp, err = c.GetSavepoint("job", queued.ID)
		if err != nil || sp.Status != SavepointCompleted {
			t.Fatalf("savepoint not completed: %+v %v", sp, err)
		}
	}
	if c.queuedSavepointJobs["job"] {
		t.Fatal("empty queue retained in dispatch index")
	}
}

func TestQueuedSavepointSurvivesCoordinatorRecovery(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	if _, err := c.TriggerCheckpoint("job"); err != nil {
		t.Fatal(err)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	sp, err := c.GetSavepoint("job", queued.ID)
	if err != nil || !sp.Queued || sp.Status != SavepointInProgress || !c.queuedSavepointJobs["job"] {
		t.Fatalf("lost unstarted request: %+v %v", sp, err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	active := c.activeCheckpoints["job"]
	if active.SavepointID != queued.ID || active.EpochID != c.epoch {
		t.Fatalf("wrong recovered boundary: %+v", active)
	}
	completeQueueCheckpoint(t, c, active)
}

func TestDeletedQueuedSavepointCannotBeResurrected(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	current, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSavepoint("job", queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.AbortCheckpoint("job", current.ID, current.EpochID); err != nil {
		t.Fatal(err)
	}
	// A runner may have selected the queued ID just before DELETE acquired the
	// lock. Dispatch must recheck that the durable request still exists.
	if _, err := c.triggerCheckpointWithQueue("job", queued.ID, false, true); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("stale dispatch resurrected deleted request: %v", err)
	}
	if _, err := c.GetSavepoint("job", queued.ID); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatal("deleted request exists")
	}
}

func TestCancelingJobFailsUnstartedSavepoint(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	if _, err := c.TriggerCheckpoint("job"); err != nil {
		t.Fatal(err)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelJob("job"); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	sp, err := c.GetSavepoint("job", queued.ID)
	if err != nil || sp.Queued || sp.Status != SavepointFailed || sp.CompletionTime.IsZero() {
		t.Fatalf("orphaned queue entry: %+v %v", sp, err)
	}
}

func TestQueueActivationRetriesFailedBatch(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	current, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	completeQueueCheckpoint(t, c, *current)
	fault := &deploymentBatchStore{MetadataStore: store, fail: true}
	c.store = fault
	if err := c.advanceQueuedSavepoint("job"); err == nil {
		t.Fatal("expected activation write failure")
	}
	sp, _ := c.GetSavepoint("job", queued.ID)
	if !sp.Queued || sp.CheckpointID != 0 || len(c.activeCheckpoints) != 0 {
		t.Fatal("failed activation consumed request")
	}
	fault.fail = false
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	if c.activeCheckpoints["job"].SavepointID != queued.ID {
		t.Fatal("retry lost queued identity")
	}
}

func TestCheckpointRunnerDispatchesQueuedSavepoint(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	current, err := c.TriggerCheckpoint("job")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := c.TriggerSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	completeQueueCheckpoint(t, c, *current)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); c.runPeriodicCheckpoints(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(2 * time.Second)
	for {
		sp, err := c.GetSavepoint("job", queued.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !sp.Queued && sp.CheckpointID != 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("checkpoint runner never dispatched queued request")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestHTTPReturnsAcceptedForQueuedSavepoint(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	if _, err := c.TriggerCheckpoint("job"); err != nil {
		t.Fatal(err)
	}
	server := startTestHTTPServer(t, c)
	response, err := http.Post("http://"+server.Addr()+"/api/v1/jobs/job/savepoints", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var body savepointResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Queued || body.Status != "IN_PROGRESS" || body.ID == "" {
		t.Fatalf("queued response = %+v", body)
	}
}

type queuedSavepointNoScanStore struct{ MetadataStore }

func (*queuedSavepointNoScanStore) PrefixScan([]byte, func([]byte, []byte) bool) error {
	panic("waiting queue scanned metadata")
}
func TestWaitingSavepointQueueDoesNotScanMetadata(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	if _, err := c.TriggerCheckpoint("job"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.TriggerSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	c.store = &queuedSavepointNoScanStore{store}
	for range 10 {
		if err := c.advanceQueuedSavepoint("job"); !errors.Is(err, ErrCheckpointInProgress) {
			t.Fatalf("waiting result: %v", err)
		}
	}
}
