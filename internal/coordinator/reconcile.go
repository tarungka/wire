package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// RegisterWorkerRequest is the request payload for worker registration.
type RegisterWorkerRequest struct {
	WorkerID         string   `codec:"worker_id"`
	Address          string   `codec:"address"`
	TaskSlotsTotal   int      `codec:"task_slots_total"`
	HighestSeenEpoch uint64   `codec:"highest_seen_epoch"`
	RunningTasks     []string `codec:"running_tasks"`
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
	c.mu.RLock()
	if c.state != StateLeader {
		c.mu.RUnlock()
		return nil, ErrNotLeader
	}
	currentEpoch := c.epoch
	c.mu.RUnlock()

	// Epoch fencing: reject if the worker has seen a newer epoch.
	if req.HighestSeenEpoch > currentEpoch {
		return nil, fmt.Errorf("%w: worker epoch %d > coordinator epoch %d",
			ErrStaleEpoch, req.HighestSeenEpoch, currentEpoch)
	}

	worker := &WorkerMeta{
		ID:                 req.WorkerID,
		Address:            req.Address,
		TaskSlotsTotal:     req.TaskSlotsTotal,
		TaskSlotsAvailable: req.TaskSlotsTotal,
		LastHeartbeat:      time.Now().UTC(),
		RunningTasks:       req.RunningTasks,
	}

	if err := c.persistWorker(worker); err != nil {
		return nil, fmt.Errorf("persisting worker: %w", err)
	}

	result, err := c.reconcileTasks(req.WorkerID, req.RunningTasks)
	if err != nil {
		return nil, fmt.Errorf("reconciling tasks: %w", err)
	}

	// Mark missing tasks as FAILED in the store so they can be rescheduled.
	for _, taskID := range result.MissingTasks {
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

	result := &ReconcileResult{}

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
