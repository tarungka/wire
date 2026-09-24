package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSavepointCancelWaitsForDurableBoundary(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	job, sp, err := c.CancelJobWithSavepoint("job")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobRunning || !job.CancelAfterSavepoint {
		t.Fatal("stopped before snapshot")
	}
	_, duplicate, err := c.CancelJobWithSavepoint("job")
	if err != nil || duplicate.ID != sp.ID {
		t.Fatal("duplicate cancel changed boundary", err)
	}
	if _, _, err := c.PauseJob("job"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("conflicting pause accepted", err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].Status != JobRunning {
		t.Fatal("stopped during snapshot")
	}
	completeQueueCheckpoint(t, c, c.activeCheckpoints["job"])
	saved, err := c.GetSavepoint("job", sp.ID)
	if err != nil || saved.Status != SavepointCompleted {
		t.Fatal("missing completed boundary", err)
	}
	if c.jobs["job"].Status != JobCanceling || c.jobs["job"].SavepointPath == "" {
		t.Fatal("missing cancellation decision")
	}
	if err := c.advanceCancellation("job", time.Now().Add(c.config.WorkerTimeout)); err != nil {
		t.Fatal(err)
	}
	if c.jobs["job"].Status != JobCanceled {
		t.Fatal("cancellation did not finish")
	}
}

func TestSavepointCancelFailureLeavesJobRunning(t *testing.T) {
	c, _ := checkpointPolicyCoordinator(t)
	if _, _, err := c.CancelJobWithSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceQueuedSavepoint("job"); err != nil {
		t.Fatal(err)
	}
	cp := c.activeCheckpoints["job"]
	if err := c.AbortCheckpoint("job", cp.ID, cp.EpochID); err != nil {
		t.Fatal(err)
	}
	if err := c.advancePause("job", time.Now()); err != nil {
		t.Fatal(err)
	}
	job := c.jobs["job"]
	if job.Status != JobRunning || job.CancelAfterSavepoint || job.PauseSavepointID != "" || !strings.Contains(job.PauseFailure, "not canceled") {
		t.Fatalf("unsafe failed cancellation: %+v", job)
	}
}

func TestSavepointCancelIntentIsAtomic(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	c.store = &deploymentBatchStore{MetadataStore: store, fail: true}
	if _, _, err := c.CancelJobWithSavepoint("job"); err == nil {
		t.Fatal("expected write failure")
	}
	job := c.jobs["job"]
	points, err := c.ListSavepoints("job")
	if err != nil {
		t.Fatal(err)
	}
	if job.CancelAfterSavepoint || job.PauseSavepointID != "" || len(points) != 0 {
		t.Fatal("published undurable intent")
	}
}

func TestSavepointCancelSurvivesRecovery(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			c, _ := checkpointPolicyCoordinator(t)
			_, sp, err := c.CancelJobWithSavepoint("job")
			if err != nil {
				t.Fatal(err)
			}
			want := JobRunning
			if completed {
				if err := c.advanceQueuedSavepoint("job"); err != nil {
					t.Fatal(err)
				}
				completeQueueCheckpoint(t, c, c.activeCheckpoints["job"])
				want = JobCanceling
			}
			c.epoch++
			if err := c.recover(); err != nil {
				t.Fatal(err)
			}
			job := c.jobs["job"]
			if job.Status != want || !job.CancelAfterSavepoint || job.PauseSavepointID != sp.ID {
				t.Fatalf("lost cancel intent: %+v", job)
			}
		})
	}
}

func TestHTTPCancelSavepointQuery(t *testing.T) {
	for _, query := range []string{"true", "false", "invalid", "true&savepoint=false"} {
		t.Run(query, func(t *testing.T) {
			c, _ := checkpointPolicyCoordinator(t)
			server := startTestHTTPServer(t, c)
			response, err := http.Post("http://"+server.Addr()+"/api/v1/jobs/job/cancel?savepoint="+query, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			switch query {
			case "true":
				if response.StatusCode != http.StatusAccepted {
					t.Fatalf("status=%d", response.StatusCode)
				}
				var result pauseJobResponse
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				if result.Savepoint.ID == "" || c.jobs["job"].Status != JobRunning {
					t.Fatal("missing snapshot or early cancellation")
				}
			case "false":
				if response.StatusCode != http.StatusOK || c.jobs["job"].Status != JobCanceling {
					t.Fatal("ordinary cancel changed")
				}
			default:
				if response.StatusCode != http.StatusBadRequest || c.jobs["job"].Status != JobRunning || c.jobs["job"].PauseSavepointID != "" {
					t.Fatal("invalid query mutated job")
				}
			}
		})
	}
}
