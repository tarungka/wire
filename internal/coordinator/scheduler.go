package coordinator

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

const (
	schedulerInterval = 2 * time.Second
	totalKeyGroups    = 128
)

// runScheduler periodically scans for CREATED jobs and deploys them to workers.
func (c *Coordinator) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	c.log.Info().Msg("scheduler started")

	for {
		select {
		case <-ctx.Done():
			c.log.Info().Msg("scheduler stopping")
			return
		case <-ticker.C:
			c.scheduleTick(ctx)
		}
	}
}

// scheduleTick runs a single scheduler iteration.
func (c *Coordinator) scheduleTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	// Snapshot CREATED jobs under RLock.
	c.mu.RLock()
	var createdJobs []*JobMeta
	for _, job := range c.jobs {
		if job.Status == JobCreated {
			createdJobs = append(createdJobs, job)
		}
	}
	c.mu.RUnlock()

	for _, job := range createdJobs {
		if ctx.Err() != nil {
			return
		}
		c.scheduleJob(job)
	}
}

// scheduleJob attempts to schedule a single CREATED job.
func (c *Coordinator) scheduleJob(job *JobMeta) {
	tasks := generateTaskDescriptors(job)

	assignments, err := c.assignTasks(tasks)
	if err != nil {
		c.log.Debug().Err(err).Str("job_id", job.ID).Msg("cannot schedule job, will retry")
		return
	}

	// Build TaskAssignmentMap.
	tam := TaskAssignmentMap{
		JobID:       job.ID,
		Assignments: make(map[string]string, len(tasks)),
	}
	for workerID, wTasks := range assignments {
		for _, t := range wTasks {
			tam.Assignments[t.TaskID] = workerID
		}
	}

	// Transition CREATED → DEPLOYING and persist assignments under Lock.
	c.mu.Lock()
	// Re-check status under lock (another tick may have grabbed it).
	if job.Status != JobCreated {
		c.mu.Unlock()
		return
	}

	if err := ValidateTransition(job.Status, JobDeploying); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("invalid transition")
		return
	}

	now := time.Now().UTC()
	job.Status = JobDeploying
	job.UpdatedAt = now
	if err := c.persistJobLocked(job); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to persist job")
		return
	}

	// Persist task assignment map.
	tamData, err := protocol.EncodeMsgPack(&tam)
	if err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to encode assignments")
		return
	}
	if err := c.store.Set(JobAssignmentsKey(job.ID), tamData); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to persist assignments")
		return
	}

	// Update worker metadata: add running tasks and decrement available slots.
	for workerID, wTasks := range assignments {
		w, ok := c.workers[workerID]
		if !ok {
			continue
		}
		for _, t := range wTasks {
			w.RunningTasks = append(w.RunningTasks, t.TaskID)
			w.TaskSlotsAvailable--
		}
	}
	c.mu.Unlock()

	// Enqueue DeployTask commands (outside lock).
	for workerID, wTasks := range assignments {
		for _, t := range wTasks {
			taskData, err := protocol.EncodeMsgPack(&t)
			if err != nil {
				c.log.Error().Err(err).Str("task_id", t.TaskID).Msg("failed to encode task descriptor")
				continue
			}
			c.EnqueueCommand(workerID, rpc.WorkerCommand{
				Type:   rpc.CommandTypeDeployTask,
				JobID:  job.ID,
				TaskID: t.TaskID,
				Data:   taskData,
			})
		}
	}

	c.log.Info().
		Str("job_id", job.ID).
		Int("tasks", len(tasks)).
		Int("workers", len(assignments)).
		Msg("job scheduled")
}

// generateTaskDescriptors creates task descriptors for a job.
// MVP: single implicit "pipeline" operator with parallelism tasks.
func generateTaskDescriptors(job *JobMeta) []rpc.TaskDescriptor {
	p := job.Parallelism
	if p < 1 {
		p = 1
	}

	tasks := make([]rpc.TaskDescriptor, p)
	groupsPerTask := totalKeyGroups / p
	remainder := totalKeyGroups % p

	offset := int32(0)
	for i := 0; i < p; i++ {
		size := int32(groupsPerTask)
		if i < remainder {
			size++
		}
		tasks[i] = rpc.TaskDescriptor{
			TaskID:       fmt.Sprintf("%s/op0/%d", job.ID, i),
			OperatorID:   "op0",
			SubtaskIndex: int32(i),
			Parallelism:  int32(p),
			KeyGroup: rpc.KeyGroupRange{
				Start: offset,
				End:   offset + size - 1,
			},
		}
		offset += size
	}
	return tasks
}

// assignTasks distributes tasks across available workers.
func (c *Coordinator) assignTasks(tasks []rpc.TaskDescriptor) (map[string][]rpc.TaskDescriptor, error) {
	c.mu.RLock()
	type workerSlot struct {
		id    string
		avail int
	}
	var eligible []workerSlot
	totalAvail := 0
	for _, w := range c.workers {
		if w.TaskSlotsAvailable > 0 {
			eligible = append(eligible, workerSlot{id: w.ID, avail: w.TaskSlotsAvailable})
			totalAvail += w.TaskSlotsAvailable
		}
	}
	c.mu.RUnlock()

	if totalAvail < len(tasks) {
		return nil, fmt.Errorf("insufficient slots: need %d, have %d", len(tasks), totalAvail)
	}

	// Sort by available slots descending for greedy assignment.
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].avail > eligible[j].avail
	})

	result := make(map[string][]rpc.TaskDescriptor)
	for i, task := range tasks {
		w := eligible[i%len(eligible)]
		result[w.id] = append(result[w.id], task)
	}
	return result, nil
}
