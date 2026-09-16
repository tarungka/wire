package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// RegisterWorkerRequest is the request payload for worker registration.
type RegisterWorkerRequest struct {
	SupportsReservations bool     `codec:"slot_reservations,omitempty"`
	CheckpointAddress    string   `codec:"checkpoint_address,omitempty"`
	WorkerID             string   `codec:"worker_id"`
	Address              string   `codec:"address"`
	TaskSlotsTotal       int      `codec:"task_slots_total"`
	HighestSeenEpoch     uint64   `codec:"highest_seen_epoch"`
	RunningTasks         []string `codec:"running_tasks"`
}

// RegisterWorkerResponse is returned to a worker after registration.
type RegisterWorkerResponse struct {
	Epoch         uint64   `codec:"epoch"`
	TasksToCancel []string `codec:"tasks_to_cancel"` // orphaned tasks the worker should stop
	MissingTasks  []string `codec:"missing_tasks"`   // tasks coordinator expected but worker lost
}

// RegisterWorker handles a worker (re-)registration request.
// It validates epoch fencing, persists the worker, and reconciles tasks.
func (c *Coordinator) RegisterWorker(req RegisterWorkerRequest) (*RegisterWorkerResponse, error) {
	return c.registerWorker(req, nil, nil)
}

func (c *Coordinator) registerWorker(req RegisterWorkerRequest, peer *rpc.Client, done <-chan struct{}) (*RegisterWorkerResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	currentEpoch := c.epoch

	// Epoch fencing: reject if the worker has seen a newer epoch.
	if req.HighestSeenEpoch > currentEpoch {
		return nil, fmt.Errorf("%w: worker epoch %d > coordinator epoch %d",
			ErrStaleEpoch, req.HighestSeenEpoch, currentEpoch)
	}

	worker := &WorkerMeta{
		RPCClient: peer, RPCPeerEpoch: currentEpoch,
		SupportsReservations: req.SupportsReservations,
		CheckpointAddress:    req.CheckpointAddress,
		ID:                   req.WorkerID,
		Address:              req.Address,
		TaskSlotsTotal:       req.TaskSlotsTotal,
		TaskSlotsAvailable:   req.TaskSlotsTotal,
		LastHeartbeat:        time.Now(),
		RunningTasks:         req.RunningTasks,
	}

	workerData, err := protocol.EncodeMsgPack(worker)
	if err != nil {
		return nil, err
	}
	if err := c.store.Set(WorkerMetaKey(worker.ID), workerData); err != nil {
		return nil, fmt.Errorf("persisting worker: %w", err)
	}

	c.workers[worker.ID] = worker
	// Bind the session in the same critical section as registration; an older
	// concurrent registration must never attach its peer to a newer worker.
	if peer != nil && done != nil {
		go func() {
			<-done
			c.mu.Lock()
			if current := c.workers[worker.ID]; current == worker {
				current.RPCClient = nil
			}
			c.mu.Unlock()
		}()
	}
	result, err := c.reconcileTasks(req.WorkerID, req.RunningTasks)
	if err != nil {
		return nil, fmt.Errorf("reconciling tasks: %w", err)
	}

	// Mark missing tasks as FAILED in the store so they can be rescheduled.
	for _, taskID := range result.MissingTasks {
		c.taskStatuses[taskID] = rpc.TaskStatusFailed
		if job := c.jobs[result.taskJobs[taskID]]; job != nil && (job.Status == JobRunning || job.Status == JobDeploying) {
			next := *job
			now := time.Now().UTC()
			c.resetStableRecoveryBudget(&next, now)
			next.Status = JobFailing
			next.UpdatedAt = now
			data, err := protocol.EncodeMsgPack(&next)
			if err != nil {
				return nil, err
			}
			if err := c.store.Set(JobMetaKey(job.ID), data); err != nil {
				return nil, err
			}
			*job = next
		}

		if err := c.markTaskFailed(taskID); err != nil {
			c.log.Error().Err(err).
				Str("task_id", taskID).
				Msg("failed to mark missing task as FAILED")
		}
	}

	return &RegisterWorkerResponse{
		Epoch:         currentEpoch,
		TasksToCancel: result.TasksToCancel,
		MissingTasks:  result.MissingTasks,
	}, nil
}

// ReconcileResult holds the outcome of task reconciliation between the
// coordinator's persisted assignments and a worker's reported tasks.
type ReconcileResult struct {
	taskJobs      map[string]string
	TasksToCancel []string // orphaned tasks worker should stop
	MissingTasks  []string // tasks coordinator expected but worker lost
}

// reconcileTasks compares the worker's reported running tasks against the
// coordinator's persisted task assignments and returns:
// - Orphaned tasks (worker has them but coordinator doesn't): to be canceled
// - Missing tasks (coordinator assigned them but worker doesn't have them): to be marked FAILED
func (c *Coordinator) reconcileTasks(workerID string, reportedTasks []string) (*ReconcileResult, error) {
	// Load persisted assignments for this worker.
	assignedTasks := make(map[string]bool)
	data, err := c.store.Get(WorkerTasksKey(workerID))
	if err != nil {
		return nil, err
	}
	if data != nil {
		var tasks []string
		if err := protocol.DecodeMsgPack(data, &tasks); err != nil {
			return nil, fmt.Errorf("decoding worker tasks: %w", err)
		}
		for _, t := range tasks {
			assignedTasks[t] = true
		}
	}

	reportedSet := make(map[string]bool, len(reportedTasks))
	for _, t := range reportedTasks {
		reportedSet[t] = true
	}

	result := &ReconcileResult{taskJobs: make(map[string]string)}
	// Job assignments are the scheduler's authoritative deployment record.
	for jobID, job := range c.jobs {
		if job.Status.IsTerminal() {
			continue
		}
		data, err := c.store.Get(JobAssignmentsKey(jobID))
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		var assignment TaskAssignmentMap
		if err := protocol.DecodeMsgPack(data, &assignment); err != nil {
			return nil, err
		}
		if assignment.JobID != jobID {
			return nil, fmt.Errorf("assignment job identity mismatch")
		}
		for taskID, owner := range assignment.Assignments {
			if owner == workerID {
				assignedTasks[taskID] = true
				result.taskJobs[taskID] = jobID
			}
		}
	}

	// Find orphaned tasks: reported by worker but not in coordinator's records.
	for _, t := range reportedTasks {
		if !assignedTasks[t] {
			result.TasksToCancel = append(result.TasksToCancel, t)
		}
	}

	// Find missing tasks: assigned by coordinator but not on worker.
	for t := range assignedTasks {
		if !reportedSet[t] {
			c.log.Warn().
				Str("worker_id", workerID).
				Str("task_id", t).
				Msg("missing task on worker, marking FAILED")
			result.MissingTasks = append(result.MissingTasks, t)
		}
	}

	return result, nil
}
