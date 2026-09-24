package coordinator

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func slotReleaseJob(t *testing.T, id string) *JobMeta {
	t.Helper()
	return &JobMeta{ID: id, Status: JobCreated, Parallelism: 1, Config: encode(t, linearGraph())}
}

// assignedTask returns the single task and attempt persisted for a deployed job.
func assignedTask(t *testing.T, store *MemoryStore, jobID string) (string, string) {
	t.Helper()
	data, err := store.Get(JobAssignmentsKey(jobID))
	if err != nil {
		t.Fatal(err)
	}
	var assignment TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
		t.Fatal(err)
	}
	if len(assignment.Assignments) != 1 {
		t.Fatalf("assignments: %+v", assignment.Assignments)
	}
	for taskID := range assignment.Assignments {
		return taskID, assignment.AttemptID
	}
	return "", ""
}

func sendTaskStatus(t *testing.T, c *Coordinator, jobID, taskID, attemptID string, status rpc.TaskStatus) bool {
	t.Helper()
	payload := encode(t, rpc.UpdateTaskStatusRequest{AttemptID: attemptID, WorkerID: "worker", JobID: jobID, TaskID: taskID, EpochID: c.epoch, Status: status})
	result, rpcErr := c.HandleUpdateTaskStatus(context.Background(), 1, payload)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	return result.(*rpc.UpdateTaskStatusResponse).Accepted
}

func takeSchedulerKick(c *Coordinator) bool {
	select {
	case <-c.schedulerKick:
		return true
	default:
		return false
	}
}

func TestTerminalTaskStatusReleasesSlotAndKicksScheduler(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 1, TaskSlotsAvailable: 1, LastHeartbeat: time.Now()}
	first, queued := slotReleaseJob(t, "first"), slotReleaseJob(t, "queued")
	c.jobs[first.ID], c.jobs[queued.ID] = first, queued
	c.scheduleJob(first)
	c.scheduleJob(queued)
	if first.Status != JobDeploying || queued.Status != JobCreated {
		t.Fatalf("setup: first=%v queued=%v", first.Status, queued.Status)
	}
	worker := c.workers["worker"]
	if worker.TaskSlotsAvailable != 0 {
		t.Fatalf("setup: available slots = %d, want 0", worker.TaskSlotsAvailable)
	}
	takeSchedulerKick(c)

	taskID, attemptID := assignedTask(t, store, first.ID)
	if !sendTaskStatus(t, c, first.ID, taskID, attemptID, rpc.TaskStatusRunning) {
		t.Fatal("running status rejected")
	}
	if worker.TaskSlotsAvailable != 0 || takeSchedulerKick(c) {
		t.Fatal("running status released capacity")
	}
	if !sendTaskStatus(t, c, first.ID, taskID, attemptID, rpc.TaskStatusFinished) {
		t.Fatal("finished status rejected")
	}
	// No heartbeat has arrived: the terminal status alone must return the slot.
	if worker.TaskSlotsAvailable != 1 {
		t.Fatalf("available slots = %d after task finished, want 1", worker.TaskSlotsAvailable)
	}
	if slices.Contains(worker.RunningTasks, taskID) {
		t.Fatalf("finished task still listed as running: %v", worker.RunningTasks)
	}
	if !takeSchedulerKick(c) {
		t.Fatal("released slot did not wake the scheduler")
	}
	c.scheduleTick(context.Background())
	if queued.Status != JobDeploying {
		t.Fatalf("queued job status = %v after release, want DEPLOYING", queued.Status)
	}
}

func TestTaskSlotReleaseIsFencedAndIdempotent(t *testing.T) {
	for _, status := range []rpc.TaskStatus{rpc.TaskStatusFailed, rpc.TaskStatusCanceled} {
		t.Run(status.String(), func(t *testing.T) {
			c, store := newTestCoordinator(t)
			c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 2, TaskSlotsAvailable: 2, LastHeartbeat: time.Now()}
			job := slotReleaseJob(t, "job")
			c.jobs[job.ID] = job
			c.scheduleJob(job)
			worker := c.workers["worker"]
			// Model another busy task, so an extra release would be observable
			// below the worker's total instead of hidden by the cap.
			worker.TaskSlotsAvailable = 0
			takeSchedulerKick(c)
			taskID, attemptID := assignedTask(t, store, job.ID)

			if sendTaskStatus(t, c, job.ID, taskID, "previous-attempt", status) {
				t.Fatal("stale attempt accepted")
			}
			if worker.TaskSlotsAvailable != 0 || !slices.Contains(worker.RunningTasks, taskID) || takeSchedulerKick(c) {
				t.Fatal("stale attempt released capacity")
			}

			if !sendTaskStatus(t, c, job.ID, taskID, attemptID, status) {
				t.Fatal("current attempt rejected")
			}
			if worker.TaskSlotsAvailable != 1 || slices.Contains(worker.RunningTasks, taskID) || !takeSchedulerKick(c) {
				t.Fatalf("terminal status did not release once: available=%d running=%v", worker.TaskSlotsAvailable, worker.RunningTasks)
			}

			// The job is FAILING, not terminal, so a redelivered report is accepted.
			if !sendTaskStatus(t, c, job.ID, taskID, attemptID, status) {
				t.Fatal("duplicate report rejected")
			}
			if worker.TaskSlotsAvailable != 1 || takeSchedulerKick(c) {
				t.Fatalf("duplicate report released capacity again: available=%d", worker.TaskSlotsAvailable)
			}
		})
	}
}

func TestHeartbeatKicksSchedulerOnlyWhenCapacityGrows(t *testing.T) {
	c, _ := newTestCoordinator(t)
	c.workers["worker"] = &WorkerMeta{ID: "worker", TaskSlotsTotal: 2, TaskSlotsAvailable: 0, LastHeartbeat: time.Now()}
	heartbeat := func(active int32) {
		t.Helper()
		payload := encode(t, rpc.HeartbeatRequest{WorkerID: "worker", EpochID: c.epoch, Load: &rpc.WorkerLoad{ActiveSlots: active, TotalSlots: 2}})
		result, rpcErr := c.HandleHeartbeat(context.Background(), 1, payload)
		if rpcErr != nil || !result.(*rpc.HeartbeatResponse).Accepted {
			t.Fatalf("heartbeat: %v %v", result, rpcErr)
		}
	}
	heartbeat(2)
	if takeSchedulerKick(c) {
		t.Fatal("unchanged capacity woke the scheduler")
	}
	heartbeat(1)
	if c.workers["worker"].TaskSlotsAvailable != 1 || !takeSchedulerKick(c) {
		t.Fatal("heartbeat that freed a slot did not wake the scheduler")
	}
	heartbeat(1)
	if takeSchedulerKick(c) {
		t.Fatal("repeated capacity woke the scheduler")
	}
	heartbeat(2)
	if c.workers["worker"].TaskSlotsAvailable != 0 || takeSchedulerKick(c) {
		t.Fatal("reduced capacity woke the scheduler")
	}
}

func TestLateTerminalReleaseDoesNotDoubleCountHeartbeatCapacity(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.workers["worker"] = &WorkerMeta{ID: "worker", Address: "worker:1", TaskSlotsTotal: 2, TaskSlotsAvailable: 2, LastHeartbeat: time.Now()}
	stale, second, third := slotReleaseJob(t, "stale"), slotReleaseJob(t, "second"), slotReleaseJob(t, "third")
	c.jobs[stale.ID], c.jobs[second.ID], c.jobs[third.ID] = stale, second, third
	c.scheduleJob(stale)
	staleTask, staleAttempt := assignedTask(t, store, stale.ID)

	// The worker's terminal RPC timed out locally, so it dropped the task and
	// its next heartbeat already advertises the freed slot.
	payload := encode(t, rpc.HeartbeatRequest{WorkerID: "worker", EpochID: c.epoch, Load: &rpc.WorkerLoad{ActiveSlots: 0, TotalSlots: 2}})
	if result, rpcErr := c.HandleHeartbeat(context.Background(), 1, payload); rpcErr != nil || !result.(*rpc.HeartbeatResponse).Accepted {
		t.Fatalf("heartbeat: %v %v", result, rpcErr)
	}
	c.scheduleJob(second)
	c.scheduleJob(third)
	worker := c.workers["worker"]
	if second.Status != JobDeploying || third.Status != JobDeploying || worker.TaskSlotsAvailable != 0 {
		t.Fatalf("setup: second=%v third=%v available=%d", second.Status, third.Status, worker.TaskSlotsAvailable)
	}

	// The delayed report for the dropped task finally arrives.
	if !sendTaskStatus(t, c, stale.ID, staleTask, staleAttempt, rpc.TaskStatusFinished) {
		t.Fatal("delayed terminal report rejected")
	}
	if slices.Contains(worker.RunningTasks, staleTask) {
		t.Fatalf("stale task still listed: %v", worker.RunningTasks)
	}
	// Both slots are held by the second and third jobs; the heartbeat already
	// returned the stale task's slot, so it must not be credited again.
	if worker.TaskSlotsAvailable != 0 {
		t.Fatalf("available slots = %d, want 0: slot credited by both heartbeat and terminal report", worker.TaskSlotsAvailable)
	}
}
