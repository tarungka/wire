package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func reservationPayload(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := protocol.EncodeMsgPack(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWorkerReservationsBoundExpireAndFence(t *testing.T) {
	w := New(Config{TaskSlots: 2}, zerolog.Nop())
	w.epoch = 5
	req := rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 5, ReservationID: "attempt", RequiredSlots: 2}
	reserve := func(req rpc.RequestTaskSlotsRequest) (*rpc.RequestTaskSlotsResponse, *rpc.RPCError) {
		res, err := w.handleRequestTaskSlots(context.Background(), 1, reservationPayload(t, req))
		if err != nil {
			return nil, err
		}
		return res.(*rpc.RequestTaskSlotsResponse), nil
	}
	first, err := reserve(req)
	if err != nil || first.Granted != 2 {
		t.Fatalf("reservation: %v %v", first, err)
	}
	duplicate, err := reserve(req)
	if err != nil || duplicate.ExpiresAtMs != first.ExpiresAtMs {
		t.Fatal("retry changed reservation")
	}
	other := req
	other.ReservationID = "other"
	if _, err := reserve(other); err == nil || err.Code != rpc.ErrCodeInsufficientSlots {
		t.Fatalf("overbooked: %v", err)
	}
	stale := req
	stale.EpochID = 4
	if _, err := reserve(stale); err == nil || err.Code != rpc.ErrCodeStaleEpoch {
		t.Fatalf("stale reservation accepted: %v", err)
	}
	w.mu.Lock()
	w.reservations["attempt"].expires = time.Now().Add(-time.Second)
	w.mu.Unlock()
	if _, err := reserve(other); err != nil {
		t.Fatalf("expired slots unavailable: %v", err)
	}
	other.Release = true
	if _, err := reserve(other); err != nil {
		t.Fatal(err)
	}
	query := rpc.RequestTaskSlotsRequest{EpochID: 5}
	if res, err := reserve(query); err != nil || res.AvailableSlots != 2 {
		t.Fatalf("release: %v %v", res, err)
	}
}

func TestWorkerCancelledReservationCannotDeployLate(t *testing.T) {
	w := New(Config{TaskSlots: 1, WorkerID: "worker"}, zerolog.Nop())
	w.epoch = 5
	reserve := rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 5, ReservationID: "attempt", RequiredSlots: 1}
	if _, err := w.handleRequestTaskSlots(context.Background(), 1, reservationPayload(t, reserve)); err != nil {
		t.Fatal(err)
	}
	w.handleCommands([]rpc.WorkerCommand{{Type: rpc.CommandTypeCancelTask, JobID: "job", TaskID: "task", EpochID: 5, AttemptID: "attempt"}})
	req := rpc.SubmitJobRequest{JobID: "job", EpochID: 5, AttemptID: "attempt", ReservationID: "attempt", Tasks: []rpc.TaskDescriptor{{TaskID: "task", EpochID: 5, AttemptID: "attempt"}}}
	if _, err := w.handleSubmitJob(context.Background(), 1, reservationPayload(t, req)); err == nil || err.Code != rpc.ErrCodeInvalidTransition {
		t.Fatalf("late deployment admitted: %v", err)
	}
	if len(w.tasks) != 0 {
		t.Fatal("task admitted")
	}
}

func TestReservedDeploymentRetryDoesNotReexecuteCompletedTask(t *testing.T) {
	var running atomic.Bool
	source := &lifecycleSource{remaining: 1, running: &running}
	r, desc := lifecyclePipeline(source, &lifecycleMap{}, &lifecycleSink{})
	w, reports := workerWithStatusCapture(t, r, &running)
	w.cfg.TaskSlots = 1
	w.epoch = 5
	desc.TaskID = "task"
	desc.EpochID = 5
	desc.AttemptID = "attempt"
	reserve := rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 5, ReservationID: "attempt", RequiredSlots: 1}
	if _, err := w.handleRequestTaskSlots(context.Background(), 1, reservationPayload(t, reserve)); err != nil {
		t.Fatal(err)
	}
	req := rpc.SubmitJobRequest{JobID: "job", EpochID: 5, AttemptID: "attempt", ReservationID: "attempt", Tasks: []rpc.TaskDescriptor{desc}}
	raw := reservationPayload(t, req)
	if _, err := w.handleSubmitJob(context.Background(), 2, raw); err != nil {
		t.Fatal(err)
	}
	for _, want := range []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFinished} {
		select {
		case report := <-reports:
			if report.Status != want {
				t.Fatalf("status=%v want=%v", report.Status, want)
			}
		case <-time.After(time.Second):
			t.Fatal("task did not complete")
		}
	}
	w.mu.RLock()
	h := w.tasks["task"]
	w.mu.RUnlock()
	if h != nil {
		select {
		case <-h.done:
		case <-time.After(time.Second):
			t.Fatal("task did not tear down")
		}
	}
	if _, err := w.handleSubmitJob(context.Background(), 3, raw); err != nil {
		t.Fatalf("lost reply retry: %v", err)
	}
	w.mu.RLock()
	count := len(w.tasks)
	w.mu.RUnlock()
	if count != 0 || source.opened.Load() != 1 {
		t.Fatal("completed attempt re-executed")
	}
	req.Tasks[0].OperatorID = "different"
	if _, err := w.handleSubmitJob(context.Background(), 4, reservationPayload(t, req)); err == nil || err.Code != rpc.ErrCodeDuplicateTask {
		t.Fatalf("conflicting attempt: %v", err)
	}
}

func TestTriggerCheckpointRPCIsIdempotentAndFenced(t *testing.T) {
	w := New(Config{TaskSlots: 1}, zerolog.Nop())
	w.epoch = 5
	cp := &taskCheckpointRuntime{source: true, triggers: make(chan engine.CheckpointTrigger, 1)}
	w.tasks["task"] = &taskHandle{jobID: "job", epoch: 5, checkpoint: cp}
	req := rpc.TriggerCheckpointRequest{JobID: "job", CheckpointID: 7, EpochID: 5}
	for i := 0; i < 2; i++ {
		if _, err := w.handleTriggerCheckpoint(context.Background(), 1, reservationPayload(t, req)); err != nil {
			t.Fatal(err)
		}
	}
	if len(cp.triggers) != 1 {
		t.Fatal("duplicate trigger enqueued")
	}
	req.EpochID = 4
	if _, err := w.handleTriggerCheckpoint(context.Background(), 1, reservationPayload(t, req)); err == nil || err.Code != rpc.ErrCodeStaleEpoch {
		t.Fatalf("stale trigger accepted: %v", err)
	}
	delete(w.tasks, "task")
	req.EpochID = 5
	if _, err := w.handleTriggerCheckpoint(context.Background(), 1, reservationPayload(t, req)); err == nil || err.Code != rpc.ErrCodeTaskNotRunning {
		t.Fatalf("absent task accepted: %v", err)
	}
}
