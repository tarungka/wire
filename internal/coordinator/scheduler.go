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
	c.jobStatusCounts[job.Status]--
	c.jobStatusCounts[JobDeploying]++
	c.unindexJobByStatus(job.Status, job.ID)
	c.indexJobByStatus(JobDeploying, job)
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
	// Populate the in-memory cache so allTasksInStatus and CancelJob
	// avoid re-Get + re-DecodeMsgPack on every UpdateTaskStatus.
	c.assignments[job.ID] = tam

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

// generateTaskDescriptors creates task descriptors for a job by decoding
// the persisted JobGraph (stored verbatim as job.Config, msgpack-encoded)
// and producing one descriptor per subtask.
//
// Phase 1: linear pipelines only. Every subtask carries the full operator
// chain (topo-sorted from the graph). Cross-worker shuffle comes in Phase 2,
// at which point this splits the graph at shuffle boundaries and populates
// Upstream/Downstream channel info.
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
	if hasShuffleEdge(graph) {
		return nil, fmt.Errorf("job %q has shuffle edges; cross-worker shuffle is not yet supported (Phase 2)", job.ID)
	}

	p := job.Parallelism
	if p < 1 {
		p = 1
	}

	tasks := make([]rpc.TaskDescriptor, p)
	groupsPerTask := totalKeyGroups / p
	remainder := totalKeyGroups % p

	// The task's OperatorID is the first non-source operator's ID, or the
	// source itself if the chain is source-only. This preserves the
	// "one task per subtask of a logical operator" shape for Phase 2.
	primaryOpID := sorted[0].OperatorID
	for _, od := range sorted {
		if od.Type != rpc.OperatorTypeSource {
			primaryOpID = od.OperatorID
			break
		}
	}

	offset := int32(0)
	for i := 0; i < p; i++ {
		size := int32(groupsPerTask)
		if i < remainder {
			size++
		}
		tasks[i] = rpc.TaskDescriptor{
			TaskID:       fmt.Sprintf("%s/%s/%d", job.ID, primaryOpID, i),
			OperatorID:   primaryOpID,
			SubtaskIndex: int32(i),
			Parallelism:  int32(p),
			KeyGroup: rpc.KeyGroupRange{
				Start: offset,
				End:   offset + size - 1,
			},
			OperatorChain: sorted,
		}
		offset += size
	}
	return tasks, nil
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

// hasShuffleEdge returns true if any edge requires partitioning (Hash /
// Rebalance / Broadcast). Forward edges are safe for Phase 1.
func hasShuffleEdge(graph rpc.JobGraph) bool {
	for _, edge := range graph.Edges {
		switch edge.Shuffle {
		case rpc.ShuffleStrategyHash, rpc.ShuffleStrategyRebalance, rpc.ShuffleStrategyBroadcast:
			return true
		}
	}
	return false
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
