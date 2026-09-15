package coordinator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tarungka/wire/internal/engine"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

const (
	schedulerInterval = 2 * time.Second
)

// runScheduler periodically scans for CREATED jobs and deploys them to workers.
//
// The ticker is the fallback poll for cases where a wake-up was missed
// (e.g. a job became schedulable because a worker registered, not because
// a job was submitted). The hot path goes through Coordinator.kickScheduler
// — submitting a job notifies the scheduler so dispatch is bounded by a
// single goroutine wake-up rather than the tick interval.
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
			c.expireCheckpoints(time.Now())
			c.scheduleTick(ctx)
		case <-c.schedulerKick:
			// Coalesce a short burst of submissions into one tick. The
			// pause is small enough that interactive job latency is
			// dominated by Pebble fsync (~6 ms), but large enough that
			// a thundering herd of same-name submits all reach the
			// duplicate-check inside SubmitJob before the scheduler
			// mutates the first job's status.
			select {
			case <-time.After(10 * time.Millisecond):
			case <-ctx.Done():
				return
			}
			// Drain any extra kicks that piled up during the window.
			for drained := false; !drained; {
				select {
				case <-c.schedulerKick:
				default:
					drained = true
				}
			}
			c.scheduleTick(ctx)
		}
	}
}

// kickScheduler nudges the scheduler to run immediately rather than wait
// for the next tick. Non-blocking — drops the wake-up if one is already
// pending (the buffered channel coalesces bursts).
func (c *Coordinator) kickScheduler() {
	select {
	case c.schedulerKick <- struct{}{}:
	default:
	}
}

// scheduleTick runs a single scheduler iteration.
func (c *Coordinator) scheduleTick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	c.detectLostTaskWorkers()

	// Snapshot CREATED jobs under RLock.
	c.mu.RLock()
	var createdJobs []*JobMeta
	for _, job := range c.jobs {
		if job.Status == JobCreated || job.Status == JobFailing {
			createdJobs = append(createdJobs, job)
		}
	}
	c.mu.RUnlock()

	for _, job := range createdJobs {
		if ctx.Err() != nil {
			return
		}
		c.mu.RLock()
		failing := job.Status == JobFailing
		c.mu.RUnlock()
		if failing && !c.prepareTaskRestart(job) {
			continue
		}
		c.scheduleJob(job)
	}
}

// scheduleJob attempts to schedule a single CREATED job.
func (c *Coordinator) scheduleJob(job *JobMeta) {
	tasks, err := generateTaskDescriptors(job)
	if err != nil {
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("cannot generate task descriptors; failing job")
		// Permanent failure (e.g. malformed graph from a legacy submission).
		// Walk Created -> Failing -> Failed so the job ends in a terminal
		// state and never re-enters the scheduler queue.
		if terr := c.transitionJob(job, JobFailing); terr != nil {
			c.log.Warn().Err(terr).Str("job_id", job.ID).Msg("could not transition to FAILING")
			return
		}
		if terr := c.transitionJob(job, JobFailed); terr != nil {
			c.log.Warn().Err(terr).Str("job_id", job.ID).Msg("could not finalize FAILED transition")
		}
		return
	}

	assignments, err := c.assignTasks(tasks)
	if err != nil {
		c.recordRescalePlacementFailure(job, time.Now())
		c.log.Debug().Err(err).Str("job_id", job.ID).Msg("cannot schedule job, will retry")
		return
	}

	var attempt [16]byte
	if _, err := rand.Read(attempt[:]); err != nil {
		c.log.Error().Err(err).Msg("cannot generate deployment attempt identity")
		return
	}
	// Build TaskAssignmentMap.
	tam := TaskAssignmentMap{
		AttemptID:   hex.EncodeToString(attempt[:]),
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
	if (job.Status != JobCreated && job.Status != JobFailing) || !c.assignmentsLiveLocked(assignments, time.Now()) {
		c.mu.Unlock()
		return
	}

	if err := ValidateTransition(job.Status, JobDeploying); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("invalid transition")
		return
	}

	if err := c.attachTaskAddressesLocked(assignments); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("cannot resolve task streams")
		return
	}

	if err := c.attachCheckpointRestoreLocked(job, assignments); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("cannot deploy checkpoint recovery")
		if errors.Is(err, errNoValidCheckpoint) || errors.Is(err, engine.ErrUnsupportedSchemaVersion) {
			c.mu.RLock()
			status := job.Status
			c.mu.RUnlock()
			if status != JobFailing {
				if err := c.transitionJob(job, JobFailing); err != nil {
					return
				}
			}
			if err := c.transitionJob(job, JobFailed); err != nil {
				c.log.Warn().Err(err).Msg("cannot finalize unrecoverable job")
			}
		}
		return
	}

	// Persist fetch grants atomically with task ownership before deployment.
	tam.RestoreCheckpoints = make(map[string]rpc.CheckpointRestoreDescriptor)
	tam.RescaleParts = make(map[string][]RescaleStatePart)
	for _, workerTasks := range assignments {
		for _, task := range workerTasks {
			if task.RestoreCheckpoint != nil {
				tam.RestoreCheckpoints[task.TaskID] = *task.RestoreCheckpoint
			}
			if task.RestoreRescale != nil {
				tam.RescaleParts[task.TaskID] = task.RestoreRescale.Parts
			}
		}
	}

	// Persist the physical topology used by this deployment. A later rescale
	// must restore the old chains/ranges, not regenerate them from a new graph.
	for _, task := range tasks {
		task.Upstream, task.Downstream = nil, nil
		task.RestoreCheckpoint = nil
		task.RestoreRescale = nil
		tam.TaskDescriptors = append(tam.TaskDescriptors, task)
	}

	// Commit the state and assignments together before publishing DEPLOYING.
	tam.EpochID = c.epoch
	tam.Replicas = make(map[string]string)
	for workerID, workerTasks := range assignments {
		peer := c.checkpointPeerLocked(workerID, time.Now())
		for i := range workerTasks {
			workerTasks[i].CheckpointReplicaAddress = peer
			if peer != "" {
				tam.Replicas[workerTasks[i].TaskID] = peer
			}
		}
	}
	// One synchronous batch prevents both a second fsync under c.mu and a
	// partially persisted deployment if writing assignments fails.
	next := *job
	if job.RescaleRollback != nil {
		rollback := *job.RescaleRollback
		rollback.Attempted = true
		next.RescaleRollback = &rollback
	}
	if job.Status == JobFailing {
		if !job.RescaleRequested {
			next.RestartCount++
			next.RecoveryAttempts++
			tam.RecoveryAttemptCharged = true
		}
		next.RescaleRequested = false
	}
	next.Status = JobDeploying
	next.UpdatedAt = time.Now().UTC()
	jobData, err := protocol.EncodeMsgPack(&next)
	if err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to encode job")
		return
	}
	tamData, err := protocol.EncodeMsgPack(&tam)
	if err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to encode assignments")
		return
	}
	if err := c.store.WriteBatch([]KVPair{
		{Key: JobMetaKey(job.ID), Value: jobData},
		{Key: JobAssignmentsKey(job.ID), Value: tamData},
	}); err != nil {
		c.mu.Unlock()
		c.log.Error().Err(err).Str("job_id", job.ID).Msg("failed to persist deployment")
		return
	}
	*job = next
	for _, workerTasks := range assignments {
		for _, task := range workerTasks {
			delete(c.taskStatuses, task.TaskID)
		}
	}

	// Update worker metadata: add running tasks and decrement available slots.
	for workerID, wTasks := range assignments {
		for i := range wTasks {
			wTasks[i].EpochID = c.epoch
			wTasks[i].AttemptID = tam.AttemptID
		}
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

// checkpointPeerLocked selects one live remote replica deterministically.
// A missing endpoint leaves checkpoint replication unavailable for this task.
func (c *Coordinator) checkpointPeerLocked(sourceID string, now time.Time) string {
	source := c.workers[sourceID]
	if source == nil || source.CheckpointAddress == "" {
		return ""
	}
	var candidates []string
	for id, worker := range c.workers {
		if id != sourceID && worker.CheckpointAddress != "" && worker.CheckpointAddress != source.CheckpointAddress && !worker.LastHeartbeat.IsZero() && now.Sub(worker.LastHeartbeat) < c.config.WorkerTimeout {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ""
	}
	return c.workers[candidates[0]].CheckpointAddress
}

// generateTaskDescriptors creates task descriptors for a job by decoding
// the persisted JobGraph (stored verbatim as job.Config, msgpack-encoded)
// and producing one descriptor per subtask.
//
// Compatible forward operators are fused. Shuffle boundaries produce separate
// task chains with explicit upstream and downstream stream descriptors.
func generateTaskDescriptors(job *JobMeta) ([]rpc.TaskDescriptor, error) {
	if len(job.Config) == 0 {
		return nil, fmt.Errorf("job %q has no graph (config is empty)", job.ID)
	}

	var graph rpc.JobGraph
	if err := protocol.DecodeMsgPack(job.Config, &graph); err != nil {
		return nil, fmt.Errorf("decode job graph: %w", err)
	}

	sorted, err := topoSortOperators(graph)
	if err != nil {
		return nil, err
	}

	if len(sorted) == 0 {
		return nil, fmt.Errorf("job %q has no operators", job.ID)
	}
	p := job.Parallelism
	if p < 1 {
		p = 1
	}
	return buildPhysicalTasks(job.ID, graph, p)
}

// topoSortOperators returns the operators of the graph in topological order.
// Assumes a valid DAG; returns an error on cycles.
func topoSortOperators(graph rpc.JobGraph) ([]rpc.OperatorDescriptor, error) {
	byID := make(map[string]rpc.OperatorDescriptor, len(graph.Operators))
	inDegree := make(map[string]int, len(graph.Operators))
	adj := make(map[string][]string, len(graph.Operators))
	for _, op := range graph.Operators {
		byID[op.OperatorID] = op
		inDegree[op.OperatorID] = 0
	}
	for _, edge := range graph.Edges {
		if _, ok := byID[edge.SourceOperatorID]; !ok {
			return nil, fmt.Errorf("edge references unknown source operator %q", edge.SourceOperatorID)
		}
		if _, ok := byID[edge.TargetOperatorID]; !ok {
			return nil, fmt.Errorf("edge references unknown target operator %q", edge.TargetOperatorID)
		}
		adj[edge.SourceOperatorID] = append(adj[edge.SourceOperatorID], edge.TargetOperatorID)
		inDegree[edge.TargetOperatorID]++
	}

	// Kahn's algorithm. To keep output deterministic, push onto the ready
	// queue in insertion order of graph.Operators rather than map order.
	var queue []string
	seen := make(map[string]bool)
	for _, op := range graph.Operators {
		if inDegree[op.OperatorID] == 0 && !seen[op.OperatorID] {
			queue = append(queue, op.OperatorID)
			seen[op.OperatorID] = true
		}
	}

	result := make([]rpc.OperatorDescriptor, 0, len(graph.Operators))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		result = append(result, byID[id])
		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(result) != len(graph.Operators) {
		return nil, fmt.Errorf("job graph has a cycle")
	}
	return result, nil
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
		if w.TaskSlotsAvailable > 0 && !w.LastHeartbeat.IsZero() && time.Since(w.LastHeartbeat) < c.config.WorkerTimeout {
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

// assignmentsLiveLocked rechecks the planning snapshot before persisting a
// deployment. The caller holds c.mu; heartbeat updates use the same lock.
func (c *Coordinator) assignmentsLiveLocked(assignments map[string][]rpc.TaskDescriptor, now time.Time) bool {
	for id, tasks := range assignments {
		worker := c.workers[id]
		if worker == nil || worker.LastHeartbeat.IsZero() || now.Sub(worker.LastHeartbeat) >= c.config.WorkerTimeout || worker.TaskSlotsAvailable < len(tasks) {
			return false
		}
	}
	return true
}
