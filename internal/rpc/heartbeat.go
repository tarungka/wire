package rpc

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/logger"
)

// WorkerState represents the liveness state of a worker.
type WorkerState uint8

const (
	WorkerAlive   WorkerState = 0
	WorkerSuspect WorkerState = 1
	WorkerDead    WorkerState = 2
)

// String returns the human-readable name of the worker state.
func (s WorkerState) String() string {
	switch s {
	case WorkerAlive:
		return "Alive"
	case WorkerSuspect:
		return "Suspect"
	case WorkerDead:
		return "Dead"
	default:
		return "Unknown"
	}
}

// WorkerInfo holds the Coordinator's view of a worker's liveness.
type WorkerInfo struct {
	WorkerID      string
	Address       string
	State         WorkerState
	LastHeartbeat time.Time
	MissedCount   int
	Load          *WorkerLoad
}

// StateChangeCallback is called when a worker's state transitions.
type StateChangeCallback func(workerID string, from, to WorkerState)

// HeartbeatTracker manages Coordinator-side worker liveness tracking.
type HeartbeatTracker struct {
	mu            sync.Mutex
	cfg           Config
	workers       map[string]*WorkerInfo
	onStateChange StateChangeCallback
	log           zerolog.Logger
}

// NewHeartbeatTracker creates a new tracker with the given configuration and callback.
func NewHeartbeatTracker(cfg Config, onStateChange StateChangeCallback) *HeartbeatTracker {
	return &HeartbeatTracker{
		cfg:           cfg,
		workers:       make(map[string]*WorkerInfo),
		onStateChange: onStateChange,
		log:           logger.GetLogger("heartbeat-tracker"),
	}
}

// RegisterWorker adds a new worker as ALIVE.
func (ht *HeartbeatTracker) RegisterWorker(id, addr string) {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	ht.workers[id] = &WorkerInfo{
		WorkerID:      id,
		Address:       addr,
		State:         WorkerAlive,
		LastHeartbeat: time.Now(),
		MissedCount:   0,
	}

	ht.log.Info().Str("worker_id", id).Str("address", addr).Msg("worker registered")
}

// RecordHeartbeat resets MissedCount for the worker and transitions SUSPECT→ALIVE.
func (ht *HeartbeatTracker) RecordHeartbeat(id string, load *WorkerLoad) {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	w, ok := ht.workers[id]
	if !ok {
		return
	}

	w.MissedCount = 0
	w.LastHeartbeat = time.Now()
	w.Load = load

	if w.State == WorkerSuspect {
		old := w.State
		w.State = WorkerAlive
		if ht.onStateChange != nil {
			ht.onStateChange(id, old, WorkerAlive)
		}
		ht.log.Info().Str("worker_id", id).Msg("worker recovered to ALIVE")
	}
}

// UnregisterWorker removes a worker from tracking.
func (ht *HeartbeatTracker) UnregisterWorker(id string) {
	ht.mu.Lock()
	defer ht.mu.Unlock()
	delete(ht.workers, id)
}

// GetWorkerState returns the current state of a worker.
func (ht *HeartbeatTracker) GetWorkerState(id string) (WorkerState, bool) {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	w, ok := ht.workers[id]
	if !ok {
		return 0, false
	}
	return w.State, true
}

// GetAllWorkers returns a snapshot of all tracked workers.
func (ht *HeartbeatTracker) GetAllWorkers() []WorkerInfo {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	result := make([]WorkerInfo, 0, len(ht.workers))
	for _, w := range ht.workers {
		result = append(result, *w)
	}
	return result
}

// Run starts the heartbeat check loop. It increments MissedCount for all workers
// every HeartbeatInterval and evaluates liveness thresholds. It blocks until ctx
// is canceled.
func (ht *HeartbeatTracker) Run(ctx context.Context) {
	ticker := time.NewTicker(ht.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ht.checkWorkers()
		}
	}
}

// checkWorkers increments missed counts and evaluates thresholds.
func (ht *HeartbeatTracker) checkWorkers() {
	ht.mu.Lock()
	defer ht.mu.Unlock()

	for id, w := range ht.workers {
		if w.State == WorkerDead {
			continue
		}

		w.MissedCount++

		switch {
		case w.MissedCount >= ht.cfg.DeadThreshold && w.State == WorkerSuspect:
			old := w.State
			w.State = WorkerDead
			ht.log.Warn().
				Str("worker_id", id).
				Int("missed", w.MissedCount).
				Msg("worker transitioned to DEAD")
			if ht.onStateChange != nil {
				ht.onStateChange(id, old, WorkerDead)
			}

		case w.MissedCount >= ht.cfg.SuspectThreshold && w.State == WorkerAlive:
			old := w.State
			w.State = WorkerSuspect
			ht.log.Warn().
				Str("worker_id", id).
				Int("missed", w.MissedCount).
				Msg("worker transitioned to SUSPECT")
			if ht.onStateChange != nil {
				ht.onStateChange(id, old, WorkerSuspect)
			}
		}
	}
}

// HeartbeatSender manages Worker-side periodic heartbeat sending.
type HeartbeatSender struct {
	client           *Client
	cfg              Config
	buildRequestFn   func() *HeartbeatRequest
	handleCommandsFn func([]WorkerCommand)
	log              zerolog.Logger
}

// NewHeartbeatSender creates a sender that periodically sends heartbeats via the client.
func NewHeartbeatSender(
	client *Client,
	cfg Config,
	buildRequestFn func() *HeartbeatRequest,
	handleCommandsFn func([]WorkerCommand),
) *HeartbeatSender {
	return &HeartbeatSender{
		client:           client,
		cfg:              cfg,
		buildRequestFn:   buildRequestFn,
		handleCommandsFn: handleCommandsFn,
		log:              logger.GetLogger("heartbeat-sender"),
	}
}

// Run starts the heartbeat send loop. It blocks until ctx is canceled.
func (hs *HeartbeatSender) Run(ctx context.Context) {
	ticker := time.NewTicker(hs.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hs.sendHeartbeat(ctx)
		}
	}
}

// sendHeartbeat sends a single heartbeat and processes the response.
func (hs *HeartbeatSender) sendHeartbeat(ctx context.Context) {
	req := hs.buildRequestFn()

	resp, err := hs.client.Heartbeat(ctx, req)
	if err != nil {
		hs.log.Warn().Err(err).Msg("heartbeat failed")
		return
	}

	if len(resp.Commands) > 0 && hs.handleCommandsFn != nil {
		hs.handleCommandsFn(resp.Commands)
	}
}
