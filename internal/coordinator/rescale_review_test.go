package coordinator

import (
	"context"
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

func TestGlobalRescaleKeepsForwardBoundaryGroups(t *testing.T) {
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
			if _, err := c.RescaleJob(job.ID, "save", 8); err != nil {
				t.Fatal(err)
			}
			var actual rpc.JobGraph
			if err := protocol.DecodeMsgPack(job.Config, &actual); err != nil {
				t.Fatal(err)
			}
			for i, op := range actual.Operators {
				want := int32(4)
				if keyed && i < 2 {
					want = 1
				}
				if op.Parallelism != want {
					t.Fatalf("operator %s got %d want %d", op.OperatorID, op.Parallelism, want)
				}
			}
			if _, err := generateTaskDescriptors(job); err != nil {
				t.Fatal(err)
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
			for range 3 {
				c.scheduleTick(context.Background())
			}
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
