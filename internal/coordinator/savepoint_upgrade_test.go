package coordinator

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func stoppedUpgradeSource(t *testing.T) (*Coordinator, *MemoryStore, *JobMeta, string) {
	t.Helper()
	c, store := newTestCoordinator(t)
	config := encode(t, linearGraph())
	job := &JobMeta{ID: "old", Name: "old", Config: config, Parallelism: 1, Status: JobRunning, DeploymentGeneration: 9}
	if err := c.persistJob(job); err != nil {
		t.Fatal(err)
	}
	tasks, err := generateTaskDescriptors(job)
	if err != nil {
		t.Fatal(err)
	}
	assignment := TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old-attempt", TaskDescriptors: tasks, Assignments: map[string]string{}, Replicas: map[string]string{}}
	for _, task := range tasks {
		assignment.Assignments[task.TaskID] = "worker"
		assignment.Replicas[task.TaskID] = "replica"
	}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, assignment)); err != nil {
		t.Fatal(err)
	}
	sp, err := c.TriggerSavepoint(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if err := c.AcknowledgeCheckpoint(rpc.AcknowledgeCheckpointRequest{JobID: job.ID, TaskID: task.TaskID, WorkerID: "worker", AttemptID: "old-attempt", CheckpointID: sp.CheckpointID, EpochID: c.epoch, State: manifestState(t, task.TaskID, "replica")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.CancelJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.advanceCancellation(job.ID, time.Now().Add(c.config.WorkerTimeout)); err != nil {
		t.Fatal(err)
	}
	completed, err := c.GetSavepoint(job.ID, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c, store, job, completed.Path
}

func TestUpgradeSubmissionBatchFailurePublishesNothing(t *testing.T) {
	c, store, old, path := stoppedUpgradeSource(t)
	fault := &deploymentBatchStore{MetadataStore: store, fail: true}
	c.store = fault
	if _, err := c.SubmitJobFromSavepoint("new", 1, old.Config, path); err == nil {
		t.Fatal("expected failed persistence")
	}
	if old.UpgradeSuccessorID != "" || len(c.jobs) != 1 || c.activeJobNames["new"] != "" {
		t.Fatal("undurable successor published")
	}
	fault.fail = false
	next, err := c.SubmitJobFromSavepoint("new", 1, old.Config, path)
	if err != nil {
		t.Fatal(err)
	}
	if next.RestoreSavepoint == nil || next.TransactionJobID != old.ID || next.CheckpointIDFloor < old.LatestCheckpoint || next.DeploymentGeneration != old.DeploymentGeneration {
		t.Fatalf("missing upgrade identity: %+v", next)
	}
	c.epoch++
	if err := c.recover(); err != nil {
		t.Fatal(err)
	}
	if c.jobs[old.ID].UpgradeSuccessorID != next.ID || c.jobs[next.ID].RestoreSavepoint == nil {
		t.Fatal("recovery lost succession")
	}
	if err := c.DeleteSavepoint(old.ID, next.RestoreSavepoint.SavepointID); !errors.Is(err, ErrSavepointInUse) {
		t.Fatalf("lost source pin: %v", err)
	}
}

func TestUpgradeSubmissionAllowsOnlyOneSuccessor(t *testing.T) {
	c, _, old, path := stoppedUpgradeSource(t)
	config := append([]byte(nil), old.Config...)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"new-a", "new-b"} {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := c.SubmitJobFromSavepoint(name, 1, config, path); results <- err }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrInvalidTransition) {
			t.Fatal(err)
		}
	}
	if success != 1 || len(c.jobs) != 2 || old.UpgradeSuccessorID == "" {
		t.Fatal("multiple or missing successors")
	}
}

func TestUpgradeRejectsChangedOperatorBeforePublishing(t *testing.T) {
	c, _, old, path := stoppedUpgradeSource(t)
	graph := linearGraph()
	graph.Operators[0].OperatorID = "different"
	if _, err := c.SubmitJobFromSavepoint("new", 1, encode(t, graph), path); err == nil {
		t.Fatal("invalid operator graph accepted")
	}
	if len(c.jobs) != 1 || old.UpgradeSuccessorID != "" {
		t.Fatal("rejected graph consumed successor")
	}
}

func TestUpgradeSubmissionRacesSavepointDeletion(t *testing.T) {
	for range 10 {
		c, _, old, path := stoppedUpgradeSource(t)
		config := append([]byte(nil), old.Config...)
		points, err := c.ListSavepoints(old.ID)
		if err != nil || len(points) != 1 {
			t.Fatal("fixture savepoint missing", err)
		}
		sourceID, savepointID := old.ID, points[0].ID
		var wg sync.WaitGroup
		var submitted *JobMeta
		var submitErr, deleteErr error
		wg.Add(2)
		go func() { defer wg.Done(); submitted, submitErr = c.SubmitJobFromSavepoint("new", 1, config, path) }()
		go func() { defer wg.Done(); deleteErr = c.DeleteSavepoint(sourceID, savepointID) }()
		wg.Wait()
		if submitErr == nil {
			if submitted == nil || !errors.Is(deleteErr, ErrSavepointInUse) {
				t.Fatalf("unprotected successor: %v", deleteErr)
			}
		} else {
			if deleteErr != nil || !errors.Is(submitErr, ErrSavepointNotFound) || len(c.jobs) != 1 || old.UpgradeSuccessorID != "" {
				t.Fatalf("partial race outcome: submit=%v delete=%v", submitErr, deleteErr)
			}
		}
	}
}
