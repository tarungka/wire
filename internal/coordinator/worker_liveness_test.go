package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func TestAssignmentExcludesSilentAndRecoveredWorkers(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	c.mu.Lock()
	c.workers["recovered"] = &WorkerMeta{ID: "recovered", TaskSlotsAvailable: 10}
	c.workers["silent"] = &WorkerMeta{ID: "silent", TaskSlotsAvailable: 10, LastHeartbeat: time.Now().Add(-2 * c.config.WorkerTimeout)}
	c.mu.Unlock()
	tasks := []rpc.TaskDescriptor{{TaskID: "task"}}
	if _, err := c.assignTasks(tasks); err == nil {
		t.Fatal("assigned to workers without a fresh heartbeat")
	}
	c.mu.Lock()
	c.workers["live"] = &WorkerMeta{ID: "live", TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
	c.mu.Unlock()
	assignment, err := c.assignTasks(tasks)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignment) != 1 || len(assignment["live"]) != 1 {
		t.Fatalf("incorrect placement: %+v", assignment)
	}
}

func TestAssignmentRevalidationRejectsExpiredPlan(t *testing.T) {
	c, _ := newReadyCoordinator(t)
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workers["worker"] = &WorkerMeta{ID: "worker", TaskSlotsAvailable: 1, LastHeartbeat: now}
	plan := map[string][]rpc.TaskDescriptor{"worker": {{TaskID: "task"}}}
	if !c.assignmentsLiveLocked(plan, now) {
		t.Fatal("fresh plan rejected")
	}
	if c.assignmentsLiveLocked(plan, now.Add(c.config.WorkerTimeout)) {
		t.Fatal("expired plan accepted at timeout boundary")
	}
	c.workers["worker"].TaskSlotsAvailable = 0
	if c.assignmentsLiveLocked(plan, now) {
		t.Fatal("oversubscribed plan accepted")
	}
	delete(c.workers, "worker")
	if c.assignmentsLiveLocked(plan, now) {
		t.Fatal("removed worker accepted")
	}
}
