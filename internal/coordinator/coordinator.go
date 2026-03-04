package coordinator

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
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
		state:    StateStandby,
		nodeID:   cfg.NodeID,
		config:   cfg,
		store:    store,
		election: election,
		log:      log.With().Str("component", "coordinator").Logger(),
		jobs:     make(map[string]*JobMeta),
		workers:  make(map[string]*WorkerMeta),
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

// serve runs the main coordinator service loop: heartbeat flushing.
func (c *Coordinator) serve(ctx context.Context) error {
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
