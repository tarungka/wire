package coordinator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func rescaleReviewJob(t *testing.T, graph rpc.JobGraph, parallelism int) (*Coordinator, *JobMeta) {
	t.Helper()
	c, store := newTestCoordinator(t)
	tasks, err := buildPhysicalTasks("job", graph, parallelism)
	if err != nil {
		t.Fatal(err)
	}
	job := &JobMeta{ID: "job", Status: JobRunning, Config: encode(t, graph), Parallelism: parallelism, LatestCheckpoint: 7}
	c.jobs[job.ID] = job
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "localhost:1234", LastHeartbeat: time.Now(), TaskSlotsAvailable: 64}
	assignment := TaskAssignmentMap{JobID: job.ID, AttemptID: "old", Assignments: map[string]string{}}
	cp := CheckpointMeta{ID: 7, JobID: job.ID, EpochID: 2, Status: CheckpointCompleted, NumKeyGroups: 128, SavepointID: "save", TaskDescriptors: tasks, Tasks: map[string]string{}, Replicas: map[string]string{}, StatePaths: map[string]string{}}
	for _, task := range tasks {
		assignment.Assignments[task.TaskID] = "worker"
		c.taskStatuses[task.TaskID] = rpc.TaskStatusCanceled
		cp.Tasks[task.TaskID] = "worker"
		cp.Replicas[task.TaskID] = "replica:1"
		cp.StatePaths[task.TaskID] = "replica:1"
	}
	for _, item := range []struct {
		key   []byte
		value any
	}{
		{JobAssignmentsKey(job.ID), assignment},
		{CheckpointKey(job.ID, 7), cp},
		{SavepointKey(job.ID, "save"), SavepointMeta{ID: "save", JobID: job.ID, CheckpointID: 7, EpochID: 2, NumKeyGroups: 128, Status: SavepointCompleted}},
	} {
		if err := store.Set(item.key, encode(t, item.value)); err != nil {
			t.Fatal(err)
		}
	}
	return c, job
}

func TestGlobalRescaleRejectsUnchangedForwardGroups(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		t.Run(map[bool]string{false: "linear", true: "keyby"}[keyed], func(t *testing.T) {
			graph := linearGraph()
			if keyed {
				graph.Operators[0].Parallelism = 1
				graph.Operators[1].Parallelism = 1
				graph.Operators[1].Type = rpc.OperatorTypeKeyBy
				graph.Edges[1].Shuffle = rpc.ShuffleStrategyHash
			}
			c, job := rescaleReviewJob(t, graph, 4)
			original := string(encode(t, job))
			if _, err := c.RescaleJob(job.ID, "save", 8); !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "operators map") {
				t.Fatalf("expected actionable no-op rejection, got %v", err)
			}
			if string(encode(t, job)) != original {
				t.Fatal("no-op request mutated job")
			}
			if data, err := c.store.Get(JobMetaKey(job.ID)); err != nil || len(data) != 0 {
				t.Fatal("no-op request persisted a job change")
			}
			if len(c.DrainCommands("worker")) != 0 {
				t.Fatal("no-op request stopped the job")
			}
		})
	}
}

func TestUnplaceableRescaleRollsBackAndUsesRecoveryBudget(t *testing.T) {
	for _, exhausted := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "exhausted"}[exhausted], func(t *testing.T) {
			c, job := rescaleReviewJob(t, linearGraph(), 4)
			original := string(job.Config)
			c.workers["worker"].TaskSlotsAvailable = 4
			if exhausted {
				job.RecoveryAttempts = c.config.RestartMaxAttempts
			}
			if _, err := c.RescaleOperators(job.ID, "save", map[string]int{"src": 8, "m": 8, "snk": 8}); err != nil {
				t.Fatal(err)
			}
			c.scheduleTick(context.Background())
			// Advance the persisted placement clock, without a wall-clock sleep.
			job.RescaleRollback.PlacementFailedSince = time.Now().Add(-2 * rpc.DefaultHeartbeatInterval)
			c.scheduleTick(context.Background())
			if job.RescaleRollback == nil || !job.RescaleRollback.Attempted {
				t.Fatalf("placement retries were not bounded: job=%+v rollback=%+v", job, job.RescaleRollback)
			}
			c.scheduleTick(context.Background())
			if string(job.Config) != original || job.RescaleCheckpoint != 0 || job.RescaleRequested || job.RescaleRollback != nil || jobResponseFromMeta(job).RescaleFailure == "" {
				t.Fatalf("rollback incomplete: %+v", job)
			}
			if exhausted {
				if job.Status != JobFailed {
					t.Fatalf("budget bypassed: %+v", job)
				}
			} else {
				if job.Status != JobDeploying || job.RecoveryAttempts != 1 {
					t.Fatalf("rollback not ordinary recovery: %+v", job)
				}
				if err := c.transitionJob(job, JobRunning); err != nil {
					t.Fatal(err)
				}
				if job.RunningSince.IsZero() || job.RescaleRollback != nil {
					t.Fatal("RUNNING did not preserve both PR behaviours")
				}
			}
		})
	}
}

func TestRescaleWaitsForFreedSlotsHeartbeat(t *testing.T) {
	c, job := rescaleReviewJob(t, linearGraph(), 8)
	worker := c.workers["worker"]
	worker.TaskSlotsTotal, worker.TaskSlotsAvailable = 12, 4
	if _, err := c.RescaleOperators(job.ID, "save", map[string]int{"src": 12, "m": 12, "snk": 12}); err != nil {
		t.Fatal(err)
	}
	c.scheduleTick(context.Background())
	first := job.RescaleRollback.PlacementFailedSince
	if first.IsZero() {
		t.Fatal("placement grace period not started")
	}
	// Even repeated scheduler kicks cannot consume a time-based grace period.
	for range 10 {
		c.scheduleTick(context.Background())
	}
	c.recordRescalePlacementFailure(job, first.Add(2*rpc.DefaultHeartbeatInterval-time.Nanosecond))
	if job.RescaleRollback.Attempted {
		t.Fatal("rolled back before two heartbeat intervals")
	}
	data, err := c.store.Get(JobMetaKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var persisted JobMeta
	if err := protocol.DecodeMsgPack(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if !persisted.RescaleRollback.PlacementFailedSince.Equal(first) {
		t.Fatal("placement grace period lost on restart")
	}
	// The next heartbeat finally publishes the slots freed by cancellation.
	result, rpcErr := c.HandleHeartbeat(context.Background(), 1, encode(t, rpc.HeartbeatRequest{WorkerID: "worker", EpochID: 5, Load: &rpc.WorkerLoad{ActiveSlots: 0}}))
	if rpcErr != nil || !result.(*rpc.HeartbeatResponse).Accepted {
		t.Fatalf("heartbeat: %v %v", result, rpcErr)
	}
	c.scheduleTick(context.Background())
	if job.Status != JobDeploying || job.RescaleFailure != "" || job.RecoveryAttempts != 0 {
		t.Fatalf("scale-up did not deploy after heartbeat: %+v", job)
	}
}
