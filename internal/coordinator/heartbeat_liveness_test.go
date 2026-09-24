package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

func TestHeartbeatRefreshesOnlyCurrentEpoch(t *testing.T) {
	for _, valid := range []bool{true, false} {
		name := "stale"
		if valid {
			name = "current"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := newTestCoordinator(t)
			old := time.Now().Add(-c.config.WorkerTimeout / 2)
			c.workers["worker"] = &WorkerMeta{ID: "worker", LastHeartbeat: old, TaskSlotsTotal: 4, TaskSlotsAvailable: 0}
			c.EnqueueCommand("worker", rpc.WorkerCommand{Type: rpc.CommandTypeDeployTask, TaskID: "pending"})
			req := rpc.HeartbeatRequest{WorkerID: "worker", EpochID: 5, Load: &rpc.WorkerLoad{ActiveSlots: 1}}
			if !valid {
				req.EpochID--
			}
			result, rpcErr := c.HandleHeartbeat(context.Background(), 1, encode(t, req))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			response := result.(*rpc.HeartbeatResponse)
			if response.Accepted != valid {
				t.Fatalf("response: %+v", response)
			}
			w := c.workers["worker"]
			if valid {
				if !w.LastHeartbeat.After(old) || w.TaskSlotsAvailable != 3 || len(response.Commands) != 1 {
					t.Fatalf("heartbeat did not refresh worker: %+v", w)
				}
			} else if !w.LastHeartbeat.Equal(old) || w.TaskSlotsAvailable != 0 || len(response.Commands) != 0 || len(c.DrainCommands("worker")) != 1 {
				t.Fatal("stale heartbeat changed liveness or consumed commands")
			}
		})
	}
}

func TestLostWorkerCannotReviveWithoutRegistration(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	c.jobs[job.ID] = job
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
	c.scheduleJob(job)
	task, _ := assignedTask(t, store, job.ID)
	c.workers["worker"].LastHeartbeat = time.Now().Add(-2 * c.config.WorkerTimeout)
	req := rpc.HeartbeatRequest{WorkerID: "worker", EpochID: c.epoch, Timestamp: time.Now().Add(time.Hour).UnixMilli()}
	for range 2 {
		value, err := c.HandleHeartbeat(context.Background(), 1, encode(t, req))
		if err != nil || value.(*rpc.HeartbeatResponse).Accepted {
			t.Fatalf("expired worker revived: %v %v", value, err)
		}
	}
	c.detectLostTaskWorkers() // Scheduler publishes the deferred recovery transition.
	if !c.workers["worker"].Lost || c.taskStatuses[task] != rpc.TaskStatusFailed || job.Status != JobFailing || c.aliveWorkerCount() != 0 {
		t.Fatal("incomplete worker-loss cascade")
	}
	if _, err := c.RegisterWorker(RegisterWorkerRequest{WorkerID: "worker", Address: "worker:1", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch}); err != nil {
		t.Fatal(err)
	}
	value, err := c.HandleHeartbeat(context.Background(), 1, encode(t, req))
	if err != nil || !value.(*rpc.HeartbeatResponse).Accepted || c.workers["worker"].Lost || c.aliveWorkerCount() != 1 {
		t.Fatalf("fresh registration not live: %v %v", value, err)
	}
}

func TestAllWorkersLostWaitsWithoutSpendingRecoveryBudget(t *testing.T) {
	c, store := newTestCoordinator(t)
	job := slotReleaseJob(t, "job")
	job.LatestCheckpoint = 7
	c.jobs[job.ID] = job
	taskID := "job/m/0"
	job.Status = JobRunning
	c.workers["worker"] = &WorkerMeta{ID: "worker", TaskSlotsTotal: 1, LastHeartbeat: time.Now().Add(-2 * c.config.WorkerTimeout)}
	if err := store.Set(JobAssignmentsKey(job.ID), encode(t, TaskAssignmentMap{JobID: job.ID, EpochID: c.epoch, AttemptID: "old", Assignments: map[string]string{taskID: "worker"}})); err != nil {
		t.Fatal(err)
	}
	checkpoint := CheckpointMeta{ID: 7, JobID: job.ID, EpochID: 2, Status: CheckpointCompleted, Tasks: map[string]string{taskID: "worker"}, Replicas: map[string]string{taskID: "replica:1"}, StatePaths: map[string]string{taskID: "replica:1"}}
	if err := store.Set(CheckpointKey(job.ID, 7), encode(t, checkpoint)); err != nil {
		t.Fatal(err)
	}
	c.taskStatuses[taskID] = rpc.TaskStatusRunning
	for range 5 {
		c.scheduleTick(context.Background())
	}
	if job.Status != JobFailing || job.RecoveryAttempts != 0 {
		t.Fatalf("no-capacity recovery consumed budget: %+v", job)
	}
	if _, err := c.RegisterWorker(RegisterWorkerRequest{WorkerID: "worker", Address: "worker:1", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch}); err != nil {
		t.Fatal(err)
	}
	c.scheduleTick(context.Background())
	if job.Status != JobDeploying || job.RecoveryAttempts != 1 {
		t.Fatalf("rejoined worker did not permit recovery: %+v", job)
	}
}

func TestRegistrationBindsLegacyAndCurrentSessionsAtomically(t *testing.T) {
	c, _ := newTestCoordinator(t)
	// Registration and session ownership share one lock for legacy workers too.
	peers := []*rpc.Client{rpc.NewClient(nil, rpc.DefaultConfig()), rpc.NewClient(nil, rpc.DefaultConfig())}
	for i, peer := range peers {
		if _, err := c.registerWorker(RegisterWorkerRequest{WorkerID: "worker", Address: "worker:1", TaskSlotsTotal: 1, HighestSeenEpoch: c.epoch, SupportsReservations: i == 1}, peer, nil, ""); err != nil {
			t.Fatal(err)
		}
		c.mu.RLock()
		got := c.workers["worker"].RPCClient
		c.mu.RUnlock()
		if got != peer {
			t.Fatal("registration did not atomically bind its own session")
		}
	}
}
