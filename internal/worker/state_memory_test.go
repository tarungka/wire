package worker

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/rpc"
)

func memoryTask(id string, limits ...int64) rpc.TaskDescriptor {
	desc := rpc.TaskDescriptor{TaskID: id, AttemptID: "attempt", EpochID: 5}
	for _, limit := range limits {
		desc.OperatorChain = append(desc.OperatorChain, rpc.OperatorDescriptor{StateBackend: &rpc.StateBackendSpec{Type: "hashmap", MaxMemoryBytes: limit}})
	}
	return desc
}

func TestStateMemoryBudgetTracksChainsAndTeardown(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	first := memoryTask("first", 40, 60)
	if err := w.checkStateMemoryLocked([]rpc.TaskDescriptor{first}, 100); err != nil {
		t.Fatal(err)
	}
	w.installTaskLocked("job", "first", first, func() {})
	next := []rpc.TaskDescriptor{memoryTask("next", 1)}
	if err := w.checkStateMemoryLocked(next, 100); err == nil {
		t.Fatal("overcommitted chain budget")
	}
	w.tasks["first"].status = rpc.TaskStatusFinished
	if err := w.checkStateMemoryLocked(next, 100); err == nil {
		t.Fatal("released memory before teardown")
	}
	delete(w.tasks, "first")
	if err := w.checkStateMemoryLocked(next, 100); err != nil {
		t.Fatal(err)
	}
	for _, desc := range []rpc.TaskDescriptor{memoryTask("negative", -1), memoryTask("overflow", math.MaxInt64, math.MaxInt64, 2)} {
		if _, err := taskStateMemory(desc); err == nil {
			t.Fatal("invalid budget accepted")
		}
	}
	if err := w.checkStateMemoryLocked([]rpc.TaskDescriptor{memoryTask("a", math.MaxInt64, math.MaxInt64), memoryTask("b", 2)}, math.MaxUint64); err == nil {
		t.Fatal("batch overflow accepted")
	}
}

func TestReservedDeploymentRejectsMemoryAtomically(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "insufficient", true: "sampling_error"}[unavailable], func(t *testing.T) {
			w := New(Config{TaskSlots: 3}, zerolog.Nop())
			w.epoch = 5
			w.stateMemoryAvailable = func(context.Context) (uint64, error) {
				// The OS sampling path must not own the worker lock.
				w.mu.Lock()
				if w.stopping {
					t.Error("unexpected shutdown")
				}
				w.mu.Unlock()
				if unavailable {
					return 0, errors.New("memory unavailable")
				}
				return 100, nil
			}
			req := rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 5, ReservationID: "attempt", RequiredSlots: 2}
			if _, err := w.handleRequestTaskSlots(context.Background(), 1, reservationPayload(t, req)); err != nil {
				t.Fatal(err)
			}
			deployment := rpc.SubmitJobRequest{JobID: "job", EpochID: 5, ReservationID: "attempt", AttemptID: "attempt", Tasks: []rpc.TaskDescriptor{memoryTask("a", 60), memoryTask("b", 60)}}
			if _, err := w.handleSubmitJob(context.Background(), 1, reservationPayload(t, deployment)); err == nil || err.Code != rpc.ErrCodeInsufficientResources {
				t.Fatalf("admission=%v", err)
			}
			if len(w.tasks) != 0 || len(w.deploymentReceipts) != 0 || w.reservations["attempt"] == nil {
				t.Fatal("rejection partially consumed deployment")
			}
		})
	}
}

func TestUnlimitedAndPebbleSkipFiniteMemoryBudget(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	w.stateMemoryAvailable = func(context.Context) (uint64, error) { t.Fatal("unexpected sample"); return 0, nil }
	desc := memoryTask("unlimited", 0)
	desc.OperatorChain = append(desc.OperatorChain, rpc.OperatorDescriptor{StateBackend: &rpc.StateBackendSpec{Type: "pebble", MaxMemoryBytes: 100}})
	if _, err := w.sampleStateMemory(context.Background(), []rpc.TaskDescriptor{desc}); err != nil {
		t.Fatal(err)
	}
	if err := w.checkStateMemoryLocked([]rpc.TaskDescriptor{desc}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStateMemoryAdmissionCannotOvercommit(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	results := make(chan error, 2)
	for _, id := range []string{"a", "b"} {
		go func(id string) {
			desc := memoryTask(id, 60)
			w.mu.Lock()
			err := w.checkStateMemoryLocked([]rpc.TaskDescriptor{desc}, 100)
			if err == nil {
				w.installTaskLocked("job", id, desc, func() {})
			}
			w.mu.Unlock()
			results <- err
		}(id)
	}
	accepted := 0
	for range 2 {
		if <-results == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("admitted %d tasks with 120 bytes against 100", accepted)
	}
}

func TestPushDeploymentRejectsMemoryBeforeStartingTask(t *testing.T) {
	w, recorder := workerWithStatusRecorder(t)
	w.epoch = 5
	w.stateMemoryAvailable = func(context.Context) (uint64, error) { return 100, nil }
	desc := memoryTask("task", 101)
	w.handleDeployTask(rpc.WorkerCommand{JobID: "job", TaskID: "task", Data: reservationPayload(t, desc)})
	w.mu.RLock()
	count := len(w.tasks)
	w.mu.RUnlock()
	if count != 0 {
		t.Fatal("over-budget push started a task")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.reports) != 1 || recorder.reports[0].Status != rpc.TaskStatusFailed {
		t.Fatalf("failure not reported: %v", recorder.reports)
	}
}
