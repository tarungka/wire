package rpc

import (
	"context"
	"errors"
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
	Resources     *ResourceReport
	LastTasks     []RunningTaskSummary
}

// StateChangeCallback is called when a worker's state transitions.
type StateChangeCallback func(workerID string, from, to WorkerState)

// WorkerLostEvent carries context about a worker that transitioned to DEAD.
type WorkerLostEvent struct {
	WorkerID      string
	LastHeartbeat time.Time
	RunningTasks  []RunningTaskSummary
	MissedCount   int
}

// WorkerLostCallback is called when a worker transitions to DEAD.
type WorkerLostCallback func(event WorkerLostEvent)

// HeartbeatTrackerOption configures optional HeartbeatTracker behavior.
type HeartbeatTrackerOption func(*HeartbeatTracker)

// WithHeartbeatMetrics attaches a metrics collector to the tracker.
func WithHeartbeatMetrics(m HeartbeatMetrics) HeartbeatTrackerOption {
	return func(ht *HeartbeatTracker) {
		ht.metrics = m
	}
}

// WithWorkerLostCallback registers a callback that fires when a worker transitions to DEAD.
func WithWorkerLostCallback(cb WorkerLostCallback) HeartbeatTrackerOption {
	return func(ht *HeartbeatTracker) {
		ht.onWorkerLost = cb
	}
}

// HeartbeatTracker manages Coordinator-side worker liveness tracking.
type HeartbeatTracker struct {
	mu            sync.Mutex
	cfg           Config
	workers       map[string]*WorkerInfo
	onStateChange StateChangeCallback
	onWorkerLost  WorkerLostCallback
	metrics       HeartbeatMetrics
	log           zerolog.Logger
}

// NewHeartbeatTracker creates a new tracker with the given configuration and callback.
func NewHeartbeatTracker(cfg Config, onStateChange StateChangeCallback, opts ...HeartbeatTrackerOption) *HeartbeatTracker {
	ht := &HeartbeatTracker{
		cfg:           cfg,
		workers:       make(map[string]*WorkerInfo),
		onStateChange: onStateChange,
		metrics:       NoopHeartbeatMetrics(),
		log:           logger.GetLogger("heartbeat-tracker"),
	}
	for _, opt := range opts {
		opt(ht)
	}
	return ht
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

// stateTransition records a pending state change to be invoked outside the lock.
type stateTransition struct {
	workerID string
	from     WorkerState
	to       WorkerState
	lostEvt  *WorkerLostEvent // non-nil only for DEAD transitions
}

// RecordHeartbeat resets MissedCount for the worker and transitions SUSPECT→ALIVE.
func (ht *HeartbeatTracker) RecordHeartbeat(id string, load *WorkerLoad, resources *ResourceReport, tasks []RunningTaskSummary) {
	var transitions []stateTransition

	ht.mu.Lock()
	w, ok := ht.workers[id]
	if ok {
		timeout := ht.cfg.CoordinatorContactTimeout
		if timeout <= 0 {
			timeout = DefaultCoordinatorContactTimeout
		}
		if w.State == WorkerDead || time.Since(w.LastHeartbeat) >= timeout {
			// A late heartbeat cannot revive expired authority between checks.
			ht.mu.Unlock()
			ht.checkWorkers()
			return
		}
		w.MissedCount = 0
		w.LastHeartbeat = time.Now()
		w.Load = load
		w.Resources = resources
		w.LastTasks = tasks

		if w.State == WorkerSuspect {
			transitions = append(transitions, stateTransition{
				workerID: id,
				from:     w.State,
				to:       WorkerAlive,
			})
			w.State = WorkerAlive
			ht.log.Info().Str("worker_id", id).Msg("worker recovered to ALIVE")
		}
	}
	ht.mu.Unlock()

	// Invoke callbacks outside lock.
	for _, t := range transitions {
		if ht.onStateChange != nil {
			ht.onStateChange(t.workerID, t.from, t.to)
		}
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

// Run evaluates elapsed receipt time every heartbeat interval until cancellation.
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

// checkWorkers uses elapsed receipt time; delayed checks cannot postpone loss.
func (ht *HeartbeatTracker) checkWorkers() { ht.checkWorkersAt(time.Now()) }

func (ht *HeartbeatTracker) checkWorkersAt(now time.Time) {
	var transitions []stateTransition

	ht.mu.Lock()
	aliveCount := 0

	for id, w := range ht.workers {
		if w.State == WorkerDead {
			continue
		}

		elapsed := now.Sub(w.LastHeartbeat)
		w.MissedCount = int(elapsed / ht.cfg.HeartbeatInterval)
		timeout := ht.cfg.CoordinatorContactTimeout
		if timeout <= 0 {
			timeout = DefaultCoordinatorContactTimeout
		}

		switch {
		case elapsed >= timeout:
			old := w.State
			w.State = WorkerDead
			ht.log.Warn().
				Str("worker_id", id).
				Int("missed", w.MissedCount).
				Msg("worker transitioned to DEAD")
			ht.metrics.IncWorkersLostTotal()
			transitions = append(transitions, stateTransition{
				workerID: id,
				from:     old,
				to:       WorkerDead,
				lostEvt: &WorkerLostEvent{
					WorkerID:      id,
					LastHeartbeat: w.LastHeartbeat,
					RunningTasks:  append([]RunningTaskSummary(nil), w.LastTasks...),
					MissedCount:   w.MissedCount,
				},
			})

		case w.MissedCount >= ht.cfg.SuspectThreshold && w.State == WorkerAlive:
			old := w.State
			w.State = WorkerSuspect
			ht.log.Warn().
				Str("worker_id", id).
				Int("missed", w.MissedCount).
				Msg("worker transitioned to SUSPECT")
			transitions = append(transitions, stateTransition{
				workerID: id,
				from:     old,
				to:       WorkerSuspect,
			})
		}

		if w.State != WorkerDead {
			aliveCount++
		}
	}

	ht.metrics.SetWorkersAlive(aliveCount)
	ht.mu.Unlock()

	// Invoke callbacks outside lock.
	for _, t := range transitions {
		if ht.onStateChange != nil {
			ht.onStateChange(t.workerID, t.from, t.to)
		}
		if t.lostEvt != nil && ht.onWorkerLost != nil {
			ht.onWorkerLost(*t.lostEvt)
		}
	}
}

// HeartbeatSender manages Worker-side periodic heartbeat sending.
type HeartbeatSender struct {
	client              *Client
	cfg                 Config
	buildRequestFn      func() *HeartbeatRequest
	handleCommandsFn    func([]WorkerCommand)
	onContactLost       func()
	onContactConfirmed  func(time.Time)
	onNewEpoch          func(uint64)
	metrics             HeartbeatMetrics
	mu                  sync.Mutex
	consecutiveFailures int
	contactLostFired    bool
	lastContact         time.Time
	log                 zerolog.Logger
}

// HeartbeatSenderOption configures optional HeartbeatSender behavior.
type HeartbeatSenderOption func(*HeartbeatSender)

// WithSenderMetrics attaches a metrics collector to the sender.
func WithSenderMetrics(m HeartbeatMetrics) HeartbeatSenderOption {
	return func(hs *HeartbeatSender) { hs.metrics = m }
}

// WithContactLostCallback registers a callback that fires once when the
// coordinator becomes unreachable (consecutive failures >= threshold).
// The callback re-arms after a successful heartbeat.
func WithContactLostCallback(fn func()) HeartbeatSenderOption {
	return func(hs *HeartbeatSender) { hs.onContactLost = fn }
}

// WithContactConfirmedCallback reports accepted heartbeats to the worker's
// process-wide watchdog, which also spans coordinator reconnect attempts.
func WithContactConfirmedCallback(fn func(time.Time)) HeartbeatSenderOption {
	return func(hs *HeartbeatSender) { hs.onContactConfirmed = fn }
}

// WithNewEpochCallback reports a newer coordinator fencing token before any
// commands are dispatched. The owner must stop executions from the old epoch.
func WithNewEpochCallback(fn func(uint64)) HeartbeatSenderOption {
	return func(hs *HeartbeatSender) { hs.onNewEpoch = fn }
}

// NewHeartbeatSender creates a sender that periodically sends heartbeats via the client.
func NewHeartbeatSender(
	client *Client,
	cfg Config,
	buildRequestFn func() *HeartbeatRequest,
	handleCommandsFn func([]WorkerCommand),
	opts ...HeartbeatSenderOption,
) *HeartbeatSender {
	hs := &HeartbeatSender{
		client:           client,
		lastContact:      time.Now(),
		cfg:              cfg,
		buildRequestFn:   buildRequestFn,
		handleCommandsFn: handleCommandsFn,
		metrics:          NoopHeartbeatMetrics(),
		log:              logger.GetLogger("heartbeat-sender"),
	}
	for _, opt := range opts {
		opt(hs)
	}
	return hs
}

// Run starts the heartbeat send loop. It blocks until ctx is canceled.
func (hs *HeartbeatSender) Run(ctx context.Context) {
	ticker := time.NewTicker(hs.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		hs.mu.Lock()
		if hs.lastContact.IsZero() {
			hs.lastContact = time.Now()
		}
		remaining := time.Until(hs.lastContact.Add(hs.contactTimeout()))
		hs.mu.Unlock()
		timer := time.NewTimer(max(0, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			hs.fireContactLost()
			return
		case <-ticker.C:
			timer.Stop()
			hs.sendHeartbeat(ctx)
		}
	}
}

func (hs *HeartbeatSender) contactTimeout() time.Duration {
	if hs.cfg.CoordinatorContactTimeout > 0 {
		return hs.cfg.CoordinatorContactTimeout
	}
	return DefaultCoordinatorContactTimeout
}

func (hs *HeartbeatSender) fireContactLost() {
	hs.mu.Lock()
	fired := hs.contactLostFired
	hs.contactLostFired = true
	hs.mu.Unlock()
	if !fired && hs.onContactLost != nil {
		hs.onContactLost()
	}
}

// sendHeartbeat sends a single heartbeat and processes the response.
func (hs *HeartbeatSender) sendHeartbeat(ctx context.Context) {
	parent := ctx
	hs.mu.Lock()
	if hs.lastContact.IsZero() {
		hs.lastContact = time.Now()
	}
	deadline := hs.lastContact.Add(hs.contactTimeout())
	hs.mu.Unlock()
	if !time.Now().Before(deadline) {
		hs.fireContactLost()
		return
	}
	callDeadline := minTime(deadline, time.Now().Add(hs.cfg.methodTimeout(MethodHeartbeat)))
	ctx, cancel := context.WithDeadline(ctx, callDeadline)
	defer cancel()
	req := hs.buildRequestFn()

	start := time.Now()
	resp, err := hs.client.Heartbeat(ctx, req)
	if parent.Err() != nil {
		return
	}
	hs.metrics.ObserveLatency(time.Since(start))
	err = heartbeatReplyDeadline(err, time.Now(), deadline)
	if err == nil && resp.EpochID > req.EpochID {
		if hs.onNewEpoch != nil {
			hs.onNewEpoch(resp.EpochID)
		}
		// A new epoch requires registration, not a contact-failure budget charge.
		return
	}
	if err == nil && !resp.Accepted {
		err = errors.New("coordinator rejected heartbeat")
	}
	if err != nil {
		hs.metrics.IncFailuresTotal()
		hs.log.Warn().Err(err).Msg("heartbeat failed")

		hs.mu.Lock()
		hs.consecutiveFailures++
		failures := hs.consecutiveFailures
		fired := hs.contactLostFired
		hs.mu.Unlock()

		if !fired && ((hs.cfg.MaxConsecutiveHeartbeatFailures > 0 && failures >= hs.cfg.MaxConsecutiveHeartbeatFailures) || !time.Now().Before(deadline)) {
			hs.fireContactLost()
		}
		return
	}

	if hs.onContactConfirmed != nil {
		hs.onContactConfirmed(start)
	}

	hs.mu.Lock()
	hs.consecutiveFailures = 0
	hs.lastContact = start
	hs.contactLostFired = false
	hs.mu.Unlock()

	if len(resp.Commands) > 0 && hs.handleCommandsFn != nil {
		hs.handleCommandsFn(resp.Commands)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// The explicit time argument makes the late-reply race testable even when the
// process resumes after a pause before the context timer goroutine can run.
func heartbeatReplyDeadline(err error, now, deadline time.Time) error {
	if err == nil && !now.Before(deadline) {
		return errors.New("heartbeat reply arrived after contact deadline")
	}
	return err
}
