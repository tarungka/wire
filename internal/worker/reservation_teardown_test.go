package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

type observedWaitContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestReservedDeploymentWaitsForPreviousTeardown(t *testing.T) {
	for _, outcome := range []string{"complete", "cancel", "expire", "fence"} {
		t.Run(outcome, func(t *testing.T) {
			var running atomic.Bool
			source := &lifecycleSource{remaining: 1, running: &running}
			registry, desc := lifecyclePipeline(source, &lifecycleMap{}, &lifecycleSink{})
			w, reports := workerWithStatusCapture(t, registry, &running)
			w.cfg.TaskSlots = 2
			w.epoch = 5
			old := &taskHandle{done: make(chan struct{}), jobID: "job", epoch: 5, attemptID: "old"}
			w.tasks["task"] = old
			desc.TaskID, desc.AttemptID, desc.EpochID = "task", "new", 5
			reservation := rpc.RequestTaskSlotsRequest{JobID: "job", EpochID: 5, ReservationID: "new", RequiredSlots: 1}
			if _, err := w.handleRequestTaskSlots(context.Background(), 1, reservationPayload(t, reservation)); err != nil {
				t.Fatal(err)
			}
			if outcome == "expire" {
				w.reservations["new"].expires = time.Now().Add(100 * time.Millisecond)
			}
			req := rpc.SubmitJobRequest{JobID: "job", EpochID: 5, AttemptID: "new", ReservationID: "new", Tasks: []rpc.TaskDescriptor{desc}}
			raw := reservationPayload(t, req)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := &observedWaitContext{Context: ctx, waiting: make(chan struct{})}
			result := make(chan *rpc.RPCError, 1)
			go func() { _, err := w.handleSubmitJob(observed, 1, raw); result <- err }()
			select {
			case err := <-result:
				t.Fatalf("did not wait for teardown: %v", err)
			case <-observed.waiting:
			case <-time.After(time.Second):
				t.Fatal("did not enter teardown wait")
			}
			w.mu.Lock()
			if w.tasks["task"] != old || w.deploymentReceipts["new"] != [32]byte{} || w.reservations["new"] == nil {
				t.Error("deployment partially admitted while waiting")
			}
			w.mu.Unlock()
			switch outcome {
			case "cancel":
				cancel()
			case "expire": // The lease timer must wake admission even without teardown.
			default:
				w.mu.Lock()
				if outcome == "fence" {
					w.cancelledAttempts = map[string]bool{"new": true}
				}
				delete(w.tasks, "task")
				close(old.done)
				w.mu.Unlock()
			}
			select {
			case err := <-result:
				if (err == nil) != (outcome == "complete") {
					t.Fatalf("outcome %s: %v", outcome, err)
				}
			case <-time.After(time.Second):
				t.Fatal("admission remained blocked")
			}
			if outcome != "complete" {
				w.mu.RLock()
				_, admitted := w.deploymentReceipts["new"]
				w.mu.RUnlock()
				if admitted || source.opened.Load() != 0 {
					t.Fatal("failed admission executed task")
				}
				return
			}
			for _, status := range []rpc.TaskStatus{rpc.TaskStatusRunning, rpc.TaskStatusFinished} {
				select {
				case report := <-reports:
					if report.Status != status {
						t.Fatalf("status = %v", report.Status)
					}
				case <-time.After(time.Second):
					t.Fatal("new execution did not finish")
				}
			}
			w.mu.RLock()
			h := w.tasks["task"]
			w.mu.RUnlock()
			if h != nil {
				select {
				case <-h.done:
				case <-time.After(time.Second):
					t.Fatal("new teardown blocked")
				}
			}
			if _, err := w.handleSubmitJob(context.Background(), 2, raw); err != nil {
				t.Fatal(err)
			}
			if source.opened.Load() != 1 {
				t.Fatal("retry executed task twice")
			}
		})
	}
}
