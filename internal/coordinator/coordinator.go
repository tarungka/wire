package coordinator

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/observability"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

// Default configuration values.
const (
	DefaultHeartbeatFlushInterval = 30 * time.Second
	DefaultWorkerTimeout          = 30 * time.Second
)

// CoordinatorConfig configures the Coordinator.
type CoordinatorConfig struct {
	DataDir                string
	NodeID                 string
	ListenAddr             string
	HeartbeatFlushInterval time.Duration
	WorkerTimeout          time.Duration
}

func (c *CoordinatorConfig) resolve() {
	if c.HeartbeatFlushInterval <= 0 {
		c.HeartbeatFlushInterval = DefaultHeartbeatFlushInterval
	}
	if c.WorkerTimeout <= 0 {
		c.WorkerTimeout = DefaultWorkerTimeout
	}
}

// Coordinator is the central coordinator for the Wire cluster. It manages
// jobs, workers, and checkpoints using a pluggable leader election backend
// and a persistent metadata store.
type Coordinator struct {
	mu       sync.RWMutex
	state    CoordinatorState
	epoch    uint64
	nodeID   string
	config   CoordinatorConfig
	store    MetadataStore
	election LeaderElection
	log      zerolog.Logger

	// In-memory caches (write-through to store).
	jobs    map[string]*JobMeta
	workers map[string]*WorkerMeta

	// activeJobNames maps a non-terminal job's name to its ID, kept in
	// sync with c.jobs. Provides O(1) duplicate-name detection in
	// SubmitJob; without it the dup check is an O(N) scan over c.jobs
	// that becomes the dominant submit-path cost once N reaches the
	// tens of thousands. See docs/trds/WIP-25.
	activeJobNames map[string]string

	// Per-worker pending command queue (coordinator → worker via heartbeat).
	// Used as a fallback when no WatchCommands push stream is registered;
	// otherwise EnqueueCommand pushes directly into cmdStreams[workerID]
	// for low-latency dispatch.
	pendingCmds map[string][]rpc.WorkerCommand

	// Active server-streaming command channels per worker (populated when
	// the worker has an open WatchCommands RPC). EnqueueCommand prefers
	// these over the pendingCmds fallback so dispatch latency is bounded
	// by a single yamux frame round-trip rather than the heartbeat tick.
	cmdStreams map[string]chan rpc.WorkerCommand

	// schedulerKick lets job submission wake the scheduler immediately
	// instead of waiting for its 2s tick. Buffered so bursts of
	// submissions coalesce into one wake-up.
	schedulerKick chan struct{}

	// Task status cache (taskID → status).
	taskStatuses map[string]rpc.TaskStatus

	// Leadership context — canceled when leadership is lost.
	leaderCtx    context.Context
	leaderCancel context.CancelFunc

	// recovered tracks whether recovery has completed.
	recovered bool
}

// New creates a new Coordinator. Pass nil for election to use single-node mode.
func New(cfg CoordinatorConfig, store MetadataStore, election LeaderElection, log zerolog.Logger) *Coordinator {
	cfg.resolve()
	return &Coordinator{
		state:          StateStandby,
		nodeID:         cfg.NodeID,
		config:         cfg,
		store:          store,
		election:       election,
		log:            log.With().Str("component", "coordinator").Logger(),
		jobs:           make(map[string]*JobMeta),
		activeJobNames: make(map[string]string),
		workers:        make(map[string]*WorkerMeta),
		pendingCmds:    make(map[string][]rpc.WorkerCommand),
		cmdStreams:     make(map[string]chan rpc.WorkerCommand),
		taskStatuses:   make(map[string]rpc.TaskStatus),
		schedulerKick:  make(chan struct{}, 1),
	}
}

// Run starts the coordinator lifecycle. It blocks until ctx is canceled
// or an unrecoverable error occurs.
func (c *Coordinator) Run(ctx context.Context) error {
	c.log.Info().Str("node_id", c.nodeID).Msg("coordinator starting")

	if c.election == nil {
		// Single-node mode: become leader immediately.
		return c.runSingleNode(ctx)
	}
	return c.runMultiNode(ctx)
}

func (c *Coordinator) runSingleNode(ctx context.Context) error {
	c.mu.Lock()
	c.state = StateLeader
	c.epoch = 1
	c.leaderCtx, c.leaderCancel = context.WithCancel(ctx)
	c.mu.Unlock()

	if err := c.recover(); err != nil {
		return err
	}

	c.log.Info().Uint64("epoch", c.epoch).Msg("leader (single-node)")
	return c.serve(ctx)
}

func (c *Coordinator) runMultiNode(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		c.mu.Lock()
		c.state = StateCandidate
		c.recovered = false
		c.mu.Unlock()

		c.log.Info().Msg("campaigning for leadership")

		lctx, err := c.election.Campaign(ctx, c.nodeID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("campaign failed: %w", err)
		}

		c.mu.Lock()
		c.state = StateLeader
		c.epoch = lctx.Epoch
		c.leaderCtx, c.leaderCancel = context.WithCancel(ctx)
		c.mu.Unlock()

		if err := c.recover(); err != nil {
			c.log.Error().Err(err).Msg("recovery failed, resigning")
			_ = c.election.Resign(ctx)
			continue
		}

		c.log.Info().Uint64("epoch", c.epoch).Msg("became leader")

		// Watch for leadership loss.
		done := make(chan struct{})
		go func() {
			select {
			case <-lctx.Ctx.Done():
			case <-ctx.Done():
			}
			close(done)
		}()

		// Serve until leadership loss or shutdown.
		serveDone := make(chan error, 1)
		serveCtx, serveCancel := context.WithCancel(ctx)
		go func() {
			serveDone <- c.serve(serveCtx)
		}()

		select {
		case <-done:
			// Leadership lost or context canceled.
			serveCancel()
			<-serveDone
			c.mu.Lock()
			c.state = StateStandby
			c.recovered = false
			c.jobs = make(map[string]*JobMeta)
			c.workers = make(map[string]*WorkerMeta)
			c.pendingCmds = make(map[string][]rpc.WorkerCommand)
			c.taskStatuses = make(map[string]rpc.TaskStatus)
			c.mu.Unlock()

			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.log.Warn().Msg("leadership lost, returning to standby")
			// Loop back to campaign again.
		case err := <-serveDone:
			serveCancel()
			return err
		}
	}
}

// recover loads state from the metadata store.
func (c *Coordinator) recover() error {
	state, err := recoverFromStore(c.store)
	if err != nil {
		return err
	}

	// Abort in-flight checkpoints found during recovery.
	for _, cp := range state.checkpointsToAbort {
		cp.Status = CheckpointAborted
		data, err := protocol.EncodeMsgPack(cp)
		if err != nil {
			return fmt.Errorf("encoding aborted checkpoint %d for job %s: %w", cp.ID, cp.JobID, err)
		}
		if err := c.store.Set(CheckpointKey(cp.JobID, cp.ID), data); err != nil {
			return fmt.Errorf("persisting aborted checkpoint %d for job %s: %w", cp.ID, cp.JobID, err)
		}
	}

	// Mark in-flight savepoints as failed (coordinator crashed mid-savepoint).
	for _, sp := range state.savepointsToFail {
		sp.Status = SavepointFailed
		data, err := protocol.EncodeMsgPack(sp)
		if err != nil {
			return fmt.Errorf("encoding failed savepoint %s for job %s: %w", sp.ID, sp.JobID, err)
		}
		if err := c.store.Set(SavepointKey(sp.JobID, sp.ID), data); err != nil {
			return fmt.Errorf("persisting failed savepoint %s for job %s: %w", sp.ID, sp.JobID, err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.jobs = state.jobs
	c.workers = state.workers
	// Rebuild the active-name index from the recovered jobs. Only
	// non-terminal jobs reserve names, matching the SubmitJob check.
	c.activeJobNames = make(map[string]string, len(state.jobs))
	for _, j := range state.jobs {
		if !j.Status.IsTerminal() {
			c.activeJobNames[j.Name] = j.ID
		}
	}
	if state.epoch > c.epoch {
		c.epoch = state.epoch
	}
	c.recovered = true

	c.log.Info().
		Int("jobs", len(state.jobs)).
		Int("workers", len(state.workers)).
		Int("checkpoints_aborted", len(state.checkpointsToAbort)).
		Int("savepoints_failed", len(state.savepointsToFail)).
		Uint64("epoch", c.epoch).
		Msg("recovery complete")

	return nil
}

// serve runs the main coordinator service loop: heartbeat flushing and scheduling.
func (c *Coordinator) serve(ctx context.Context) error {
	go c.runScheduler(ctx)

	// Register the by-status job gauge. The callback is invoked once
	// per metric scrape; safe to leave registered for the lifetime of
	// this serve loop. Unregister on exit so a future re-leadership
	// re-registers cleanly without duplicate observations.
	if reg, err := observability.RegisterJobActiveGauge(c.jobStateCounts); err != nil {
		c.log.Warn().Err(err).Msg("failed to register job-by-status gauge")
	} else if reg != nil {
		defer func() { _ = reg.Unregister() }()
	}

	ticker := time.NewTicker(c.config.HeartbeatFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.log.Info().Msg("serve loop stopping")
			return nil
		case <-ticker.C:
			if err := c.flushHeartbeats(ctx); err != nil {
				c.log.Warn().Err(err).Msg("heartbeat flush failed")
			}
		}
	}
}

// jobStateCounts returns the number of jobs in each lifecycle status,
// keyed by the JobStatus.String() name. Used by the
// wire.coordinator.jobs.by_status observable gauge — invoked once per
// scrape, so the RLock duration is bounded and uncontended in practice.
func (c *Coordinator) jobStateCounts() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	counts := make(map[string]int64, 8)
	for _, j := range c.jobs {
		counts[j.Status.String()]++
	}
	return counts
}

// EnqueueCommand routes a command to a worker. If the worker has an
// active WatchCommands push stream, the command is sent on that stream
// (non-blocking — falls back to the heartbeat queue if the channel is
// full). Otherwise it appends to the heartbeat-tick queue.
func (c *Coordinator) EnqueueCommand(workerID string, cmd rpc.WorkerCommand) {
	c.mu.Lock()
	if ch, ok := c.cmdStreams[workerID]; ok {
		c.mu.Unlock()
		select {
		case ch <- cmd:
			return
		default:
			// Push channel backed up — fall through to slice queue so the
			// next heartbeat picks it up. This is a defensive fallback;
			// in steady state the stream drains as fast as the worker
			// reads.
		}
		c.mu.Lock()
	}
	c.pendingCmds[workerID] = append(c.pendingCmds[workerID], cmd)
	c.mu.Unlock()
}

// DrainCommands returns and clears all pending commands for a worker.
func (c *Coordinator) DrainCommands(workerID string) []rpc.WorkerCommand {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmds := c.pendingCmds[workerID]
	if len(cmds) > 0 {
		delete(c.pendingCmds, workerID)
	}
	return cmds
}

// RegisterCommandStream creates a per-worker channel for the
// WatchCommands push handler. Any commands queued in pendingCmds before
// the stream opened are drained into the channel so the worker doesn't
// miss its initial backlog. The returned cleanup func removes the
// registration and closes the channel; call it from a defer.
//
// The channel buffer is intentionally small — backpressure here means
// the worker isn't reading fast enough, and EnqueueCommand falls back
// to the heartbeat slice rather than blocking the scheduler.
func (c *Coordinator) RegisterCommandStream(workerID string) (<-chan rpc.WorkerCommand, func()) {
	const bufSize = 64
	ch := make(chan rpc.WorkerCommand, bufSize)

	c.mu.Lock()
	// If a previous stream is still registered (e.g. worker reconnected),
	// close the old one so its goroutine exits.
	if old, ok := c.cmdStreams[workerID]; ok {
		close(old)
	}
	c.cmdStreams[workerID] = ch
	backlog := c.pendingCmds[workerID]
	delete(c.pendingCmds, workerID)
	c.mu.Unlock()

	// Best-effort drain of the heartbeat backlog into the new stream.
	for _, cmd := range backlog {
		select {
		case ch <- cmd:
		default:
			// Buffer full already (would only happen with a huge backlog);
			// re-queue the rest in pendingCmds.
			c.mu.Lock()
			c.pendingCmds[workerID] = append(c.pendingCmds[workerID], cmd)
			c.mu.Unlock()
		}
	}

	cleanup := func() {
		c.mu.Lock()
		// Only delete if it's still our channel — guards against the
		// reconnect-replace path above.
		if cur, ok := c.cmdStreams[workerID]; ok && cur == ch {
			delete(c.cmdStreams, workerID)
			close(ch)
		}
		c.mu.Unlock()
	}
	return ch, cleanup
}

// allTasksInStatus checks if all tasks for a job have the given status.
func (c *Coordinator) allTasksInStatus(jobID string, status rpc.TaskStatus) bool {
	// Must be called with c.mu held (at least RLock).
	data, err := c.store.Get(JobAssignmentsKey(jobID))
	if err != nil || data == nil {
		return false
	}
	var assignments TaskAssignmentMap
	if err := protocol.DecodeMsgPack(data, &assignments); err != nil {
		return false
	}
	if len(assignments.Assignments) == 0 {
		return false
	}
	for taskID := range assignments.Assignments {
		if c.taskStatuses[taskID] != status {
			return false
		}
	}
	return true
}

// flushHeartbeats persists worker heartbeat summaries to the metadata store.
// Uses a full Lock to prevent races with persistWorker: if we used RLock,
// a concurrent persistWorker could update a worker between our read and
// the WriteBatch call, and our stale snapshot would overwrite the newer state.
func (c *Coordinator) flushHeartbeats(ctx context.Context) error {
	c.mu.Lock()
	if c.state != StateLeader {
		c.mu.Unlock()
		return nil
	}

	var batch []KVPair
	for id, w := range c.workers {
		data, err := protocol.EncodeMsgPack(w)
		if err != nil {
			c.mu.Unlock()
			return fmt.Errorf("encoding worker %s: %w", id, err)
		}
		batch = append(batch, KVPair{
			Key:   WorkerMetaKey(id),
			Value: data,
		})
	}
	c.mu.Unlock()

	if len(batch) == 0 {
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	return c.store.WriteBatch(batch)
}

// persistJob writes a job to both the metadata store and the in-memory cache.
// The store write and cache update are performed under the same lock to prevent
// interleaving with concurrent writes.
func (c *Coordinator) persistJob(job *JobMeta) error {
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		return fmt.Errorf("encoding job %s: %w", job.ID, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Set(JobMetaKey(job.ID), data); err != nil {
		return fmt.Errorf("persisting job %s: %w", job.ID, err)
	}
	c.jobs[job.ID] = job
	return nil
}

// persistJobLocked writes a job to both the metadata store and the in-memory
// cache. The caller MUST hold c.mu.Lock().
func (c *Coordinator) persistJobLocked(job *JobMeta) error {
	data, err := protocol.EncodeMsgPack(job)
	if err != nil {
		return fmt.Errorf("encoding job %s: %w", job.ID, err)
	}
	if err := c.store.Set(JobMetaKey(job.ID), data); err != nil {
		return fmt.Errorf("persisting job %s: %w", job.ID, err)
	}
	c.jobs[job.ID] = job
	return nil
}

// persistWorker writes a worker to both the metadata store and the in-memory cache.
// The store write and cache update are performed under the same lock to prevent
// interleaving with concurrent writes.
func (c *Coordinator) persistWorker(worker *WorkerMeta) error {
	data, err := protocol.EncodeMsgPack(worker)
	if err != nil {
		return fmt.Errorf("encoding worker %s: %w", worker.ID, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Set(WorkerMetaKey(worker.ID), data); err != nil {
		return fmt.Errorf("persisting worker %s: %w", worker.ID, err)
	}
	c.workers[worker.ID] = worker
	return nil
}

// persistEpoch writes the current epoch to the metadata store.
// The store write and cache update are performed under the same lock to prevent
// interleaving with concurrent writes.
func (c *Coordinator) persistEpoch(epoch uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, epoch)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.store.Set(ClusterEpochKey(), buf); err != nil {
		return fmt.Errorf("persisting epoch: %w", err)
	}
	c.epoch = epoch
	return nil
}

// ListWorkers returns a copy of all registered workers.
func (c *Coordinator) ListWorkers() []WorkerMeta {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]WorkerMeta, 0, len(c.workers))
	for _, w := range c.workers {
		result = append(result, *w)
	}
	return result
}

// RemoveWorker removes a worker from the in-memory cache and metadata store
// atomically under the lock.
func (c *Coordinator) RemoveWorker(nodeID string) error {
	c.mu.Lock()
	worker, ok := c.workers[nodeID]
	if !ok {
		c.mu.Unlock()
		return ErrWorkerNotFound
	}
	delete(c.workers, nodeID)
	c.mu.Unlock()

	// Delete from store. On failure, restore the in-memory entry so
	// cache and store remain consistent.
	if err := c.store.Delete(WorkerMetaKey(nodeID)); err != nil {
		c.mu.Lock()
		c.workers[nodeID] = worker
		c.mu.Unlock()
		return fmt.Errorf("deleting worker %s from store: %w", nodeID, err)
	}

	c.log.Info().Str("node_id", nodeID).Msg("worker node removed")
	return nil
}

// markTaskFailed persists a FAILED status for a task that was expected on a
// worker but not reported during reconciliation. This enables the scheduler
// to detect and reschedule lost tasks.
func (c *Coordinator) markTaskFailed(taskID string) error {
	data, err := protocol.EncodeMsgPack(JobFailed)
	if err != nil {
		return fmt.Errorf("encoding task status: %w", err)
	}
	if err := c.store.Set(TaskStatusKey(taskID), data); err != nil {
		return fmt.Errorf("persisting task %s as FAILED: %w", taskID, err)
	}
	c.log.Info().Str("task_id", taskID).Msg("task marked FAILED (missing from worker)")
	return nil
}

// ValidateEpoch checks whether a worker's reported epoch is not ahead of the
// coordinator's current epoch. Returns ErrStaleEpoch if the coordinator is stale.
func (c *Coordinator) ValidateEpoch(workerEpoch uint64) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if workerEpoch > c.epoch {
		return fmt.Errorf("%w: worker epoch %d > coordinator epoch %d",
			ErrStaleEpoch, workerEpoch, c.epoch)
	}
	return nil
}

// CurrentEpoch returns the coordinator's current epoch.
func (c *Coordinator) CurrentEpoch() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.epoch
}

// State returns the coordinator's current state.
func (c *Coordinator) State() CoordinatorState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// IsLeader returns true if this coordinator is the active leader.
func (c *Coordinator) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == StateLeader
}

// IsReady returns true if the coordinator is the leader and has completed recovery.
func (c *Coordinator) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state == StateLeader && c.recovered
}

// GetLeaderInfo returns information about the current leader.
func (c *Coordinator) GetLeaderInfo() (*LeaderInfo, bool, error) {
	if c.election == nil {
		c.mu.RLock()
		info := &LeaderInfo{
			NodeID:  c.nodeID,
			Address: c.config.ListenAddr,
			Epoch:   c.epoch,
		}
		isSelf := c.state == StateLeader
		c.mu.RUnlock()
		return info, isSelf, nil
	}

	nodeID, addr, err := c.election.GetLeader(context.Background())
	if err != nil {
		return nil, false, err
	}

	c.mu.RLock()
	epoch := c.epoch
	isSelf := nodeID == c.nodeID
	c.mu.RUnlock()

	return &LeaderInfo{
		NodeID:  nodeID,
		Address: addr,
		Epoch:   epoch,
	}, isSelf, nil
}

// Shutdown gracefully stops the coordinator.
func (c *Coordinator) Shutdown(ctx context.Context) error {
	c.log.Info().Msg("coordinator shutting down")

	c.mu.Lock()
	cancel := c.leaderCancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if c.election != nil {
		if err := c.election.Resign(ctx); err != nil {
			c.log.Warn().Err(err).Msg("resign failed during shutdown")
		}
	}

	return nil
}
