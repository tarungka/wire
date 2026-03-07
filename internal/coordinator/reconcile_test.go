package coordinator

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

func newTestCoordinator(t *testing.T) (*Coordinator, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 5
	c.recovered = true
	c.mu.Unlock()
	t.Cleanup(func() { _ = store.Close() })
	return c, store
}

func TestRegisterWorker_Success(t *testing.T) {
	c, _ := newTestCoordinator(t)

	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if resp.Epoch != 5 {
		t.Fatalf("expected epoch 5, got %d", resp.Epoch)
	}
	if len(resp.TasksToCancel) != 0 {
		t.Fatalf("expected no tasks to cancel, got %v", resp.TasksToCancel)
	}

	// Verify worker is in cache and store.
	c.mu.RLock()
	if _, ok := c.workers["w1"]; !ok {
		t.Fatal("worker not in cache")
	}
	c.mu.RUnlock()
}

func TestRegisterWorker_EpochFencing(t *testing.T) {
	c, _ := newTestCoordinator(t)

	_, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 10, // higher than coordinator's epoch 5
	})
	if err == nil {
		t.Fatal("expected error for stale epoch")
	}
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("expected ErrStaleEpoch, got %v", err)
	}
}

func TestRegisterWorker_NotLeader(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	// State is STANDBY (default).

	_, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID: "w1",
	})
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader, got %v", err)
	}
}

func TestReconcileTasks_Match(t *testing.T) {
	c, store := newTestCoordinator(t)

	// Persist tasks for worker.
	tasks := []string{"task-1", "task-2"}
	data, _ := protocol.EncodeMsgPack(tasks)
	if err := store.Set(WorkerTasksKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Worker reports the same tasks.
	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
		RunningTasks:     []string{"task-1", "task-2"},
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if len(resp.TasksToCancel) != 0 {
		t.Fatalf("expected no tasks to cancel, got %v", resp.TasksToCancel)
	}
}

func TestReconcileTasks_Orphaned(t *testing.T) {
	c, _ := newTestCoordinator(t)

	// No persisted tasks for worker, but worker reports tasks.
	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
		RunningTasks:     []string{"orphan-1", "orphan-2"},
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	if len(resp.TasksToCancel) != 2 {
		t.Fatalf("expected 2 tasks to cancel, got %v", resp.TasksToCancel)
	}
}

func TestReconcileTasks_Missing(t *testing.T) {
	c, store := newTestCoordinator(t)

	// Coordinator has tasks assigned but worker doesn't report them.
	tasks := []string{"task-1", "task-2", "task-3"}
	data, _ := protocol.EncodeMsgPack(tasks)
	if err := store.Set(WorkerTasksKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
		RunningTasks:     []string{"task-1"}, // missing task-2 and task-3
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	// No tasks to cancel (worker only has assigned tasks).
	if len(resp.TasksToCancel) != 0 {
		t.Fatalf("expected 0 tasks to cancel, got %v", resp.TasksToCancel)
	}
}

func TestReconcileTasks_MixedScenario(t *testing.T) {
	c, store := newTestCoordinator(t)

	// Coordinator has task-1 and task-2 assigned.
	tasks := []string{"task-1", "task-2"}
	data, _ := protocol.EncodeMsgPack(tasks)
	if err := store.Set(WorkerTasksKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Worker reports task-1 (match) and task-3 (orphan).
	// task-2 is missing from worker.
	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
		RunningTasks:     []string{"task-1", "task-3"},
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}
	// task-3 is orphaned → cancel.
	if len(resp.TasksToCancel) != 1 || resp.TasksToCancel[0] != "task-3" {
		t.Fatalf("expected [task-3] to cancel, got %v", resp.TasksToCancel)
	}
}

func TestReconcileTasks_MissingTaskMarkedFailed(t *testing.T) {
	c, store := newTestCoordinator(t)

	// Coordinator has task-1, task-2, task-3 assigned to worker.
	tasks := []string{"task-1", "task-2", "task-3"}
	data, _ := protocol.EncodeMsgPack(tasks)
	if err := store.Set(WorkerTasksKey("w1"), data); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	// Worker only reports task-1: task-2 and task-3 are missing.
	resp, err := c.RegisterWorker(RegisterWorkerRequest{
		WorkerID:         "w1",
		Address:          "localhost:5001",
		TaskSlotsTotal:   4,
		HighestSeenEpoch: 5,
		RunningTasks:     []string{"task-1"},
	})
	if err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	// Verify missing tasks are reported in response.
	if len(resp.MissingTasks) != 2 {
		t.Fatalf("expected 2 missing tasks, got %d: %v", len(resp.MissingTasks), resp.MissingTasks)
	}

	// Verify missing tasks are persisted as FAILED in the store.
	for _, taskID := range resp.MissingTasks {
		val, err := store.Get(TaskStatusKey(taskID))
		if err != nil {
			t.Fatalf("store.Get task status %s: %v", taskID, err)
		}
		if val == nil {
			t.Fatalf("task %s FAILED status not persisted", taskID)
		}
	}
}

func TestRegisterWorker_ConcurrentRegistration(t *testing.T) {
	c, _ := newTestCoordinator(t)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			_, err := c.RegisterWorker(RegisterWorkerRequest{
				WorkerID:         fmt.Sprintf("w-%d", id),
				Address:          fmt.Sprintf("localhost:%d", 5000+id),
				TaskSlotsTotal:   4,
				HighestSeenEpoch: 5,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent RegisterWorker error: %v", err)
	}

	// Verify all workers registered.
	c.mu.RLock()
	if len(c.workers) != n {
		t.Fatalf("expected %d workers, got %d", n, len(c.workers))
	}
	c.mu.RUnlock()
}
