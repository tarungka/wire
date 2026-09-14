package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// ackMsg carries an acknowledgement from a task slot to the coordinator.
type ackMsg struct {
	EpochID      uint64
	applied      chan struct{}
	TaskIndex    int
	CheckpointID uint64
}

// pendingCommitInfo holds deferred commit notification data so the
// coordinator can send CtrlCommitCheckpoint outside the lock.
type pendingCommitInfo struct {
	checkpointID uint64
	epochID      uint64
}

// CheckpointCoordinator tracks per-checkpoint timeouts, triggers aborts on
// timeout, and enforces consecutive failure thresholds. It is the central
// component added by WIP-05.
type CheckpointCoordinator struct {
	mu sync.Mutex

	config  CheckpointConfig
	metrics CheckpointMetrics
	log     zerolog.Logger

	// Channels to send control messages to managed task slots.
	controlChannels []chan<- ControlMsg

	// Active checkpoint state.
	activeCheckpointID uint64
	activeEpochID      uint64
	timer              *time.Timer
	pendingACKs        map[int]bool

	// Failure tracking.
	consecutiveFailures int
	totalCheckpoints    int64
	totalFailures       int64

	// MinPause enforcement.
	lastCompletionTime      time.Time
	lastCompletedCheckpoint uint64

	// Internal communication.
	failureCh chan checkpointUploadResult
	ackCh     chan ackMsg
	triggerCh chan struct{} // signals a new checkpoint was triggered

	// Two-phase commit state for transactional sinks (WIP-10).
	sinkTxnStates map[int]*SinkTaskTxnState // key: task index
	pendingCommit *pendingCommitInfo        // deferred commit notification
}

// NewCheckpointCoordinator creates a new coordinator.
func NewCheckpointCoordinator(
	cfg CheckpointConfig,
	controlChannels []chan<- ControlMsg,
	metrics CheckpointMetrics,
	log zerolog.Logger,
) *CheckpointCoordinator {
	if metrics == nil {
		metrics = newTelemetryCheckpointMetrics("")
	}
	return &CheckpointCoordinator{
		config:          cfg,
		metrics:         metrics,
		log:             log,
		controlChannels: controlChannels,
		pendingACKs:     make(map[int]bool),
		ackCh:           make(chan ackMsg, 2*len(controlChannels)),
		failureCh:       make(chan checkpointUploadResult, max(1, 2*len(controlChannels))),
		triggerCh:       make(chan struct{}, 1),
	}
}

// RegisterTransactionalSink registers a task index as hosting a transactional
// sink. Must be called before Run(). The coordinator will send
// CtrlCommitCheckpoint and CtrlAbortTransaction to registered sinks.
func (cc *CheckpointCoordinator) RegisterTransactionalSink(taskIndex int, taskID string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.sinkTxnStates == nil {
		cc.sinkTxnStates = make(map[int]*SinkTaskTxnState)
	}
	cc.sinkTxnStates[taskIndex] = &SinkTaskTxnState{
		TaskID: taskID,
		State:  TxnActive,
	}
}

// sendCommitNotifications sends CtrlCommitCheckpoint to all registered
// transactional sink task control channels. Must be called without holding mu
// to avoid deadlock on full channels.
func (cc *CheckpointCoordinator) sendCommitNotifications(ctx context.Context, info *pendingCommitInfo) {
	cc.mu.Lock()
	sinkIndices := make([]int, 0, len(cc.sinkTxnStates))
	for idx := range cc.sinkTxnStates {
		sinkIndices = append(sinkIndices, idx)
	}
	channels := append([]chan<- ControlMsg(nil), cc.controlChannels...)
	cc.mu.Unlock()

	commitMsg := ControlMsg{
		Type:         CtrlCommitCheckpoint,
		CheckpointID: info.checkpointID,
		EpochID:      info.epochID,
	}
	for _, idx := range sinkIndices {
		if idx < len(channels) {
			select {
			case channels[idx] <- commitMsg:
			case <-ctx.Done():
				return
			}
		}
	}
}

// PendingCommits returns task indices whose LastCommittedCheckpoint is behind
// the given globally-completed checkpoint. Used during recovery to identify
// sinks that need to re-commit.
func (cc *CheckpointCoordinator) PendingCommits(globallyCompletedCheckpoint uint64) []int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	var pending []int
	for idx, state := range cc.sinkTxnStates {
		if state.LastCommittedCheckpoint < globallyCompletedCheckpoint {
			pending = append(pending, idx)
		}
	}
	return pending
}

// resolveTimeout returns the configured timeout or the default.
func (cc *CheckpointCoordinator) resolveTimeout() time.Duration {
	if cc.config.Timeout > 0 {
		return cc.config.Timeout
	}
	return DefaultCheckpointTimeout
}

// TriggerCheckpoint starts a new checkpoint with the given ID and epoch.
// It initializes tracking state and starts the timeout timer.
func (cc *CheckpointCoordinator) TriggerCheckpoint(ctx context.Context, checkpointID, epochID uint64) error {
	cc.mu.Lock()

	if cc.activeCheckpointID != 0 {
		cc.mu.Unlock()
		return fmt.Errorf("%w: checkpoint %d still active", ErrCheckpointAlreadyActive, cc.activeCheckpointID)
	}

	// MinPause enforcement.
	if cc.config.MinPause > 0 && !cc.lastCompletionTime.IsZero() {
		elapsed := time.Since(cc.lastCompletionTime)
		if elapsed < cc.config.MinPause {
			wait := cc.config.MinPause - elapsed
			cc.mu.Unlock()
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			cc.mu.Lock()
			// Re-check: another goroutine may have triggered a checkpoint while we slept.
			if cc.activeCheckpointID != 0 {
				cc.mu.Unlock()
				return fmt.Errorf("%w: checkpoint %d started during MinPause wait", ErrCheckpointAborted, cc.activeCheckpointID)
			}
		}
	}

	cc.activeCheckpointID = checkpointID
	cc.activeEpochID = epochID
	cc.pendingACKs = make(map[int]bool)
	for i := range cc.controlChannels {
		cc.pendingACKs[i] = true
	}

	cc.totalCheckpoints++

	// Start timeout timer.
	timeout := cc.resolveTimeout()
	if cc.timer != nil {
		cc.timer.Stop()
	}
	cc.timer = time.NewTimer(timeout)

	cc.log.Info().
		Uint64("checkpoint_id", checkpointID).
		Uint64("epoch_id", epochID).
		Dur("timeout", timeout).
		Msg("checkpoint triggered")

	cc.mu.Unlock()

	// Wake up Run loop to pick up the new timer.
	select {
	case cc.triggerCh <- struct{}{}:
	default:
	}

	return nil
}

// AckCheckpoint records an ACK from the given task index for the given checkpoint.
func (cc *CheckpointCoordinator) AckCheckpoint(taskIndex int, checkpointID uint64) {
	select {
	case cc.ackCh <- ackMsg{TaskIndex: taskIndex, CheckpointID: checkpointID}:
	default:
		cc.log.Warn().
			Int("task_index", taskIndex).
			Uint64("checkpoint_id", checkpointID).
			Msg("ack channel full, dropping ACK")
	}
}

// AckReplicatedCheckpoint waits until the coordinator has applied or rejected
// an epoch-fenced ACK, so a finishing task cannot cancel Run before it is read.
func (cc *CheckpointCoordinator) AckReplicatedCheckpoint(ctx context.Context, taskIndex int, id, epoch uint64) error {
	applied := make(chan struct{})
	select {
	case cc.ackCh <- ackMsg{TaskIndex: taskIndex, CheckpointID: id, EpochID: epoch, applied: applied}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-applied:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FailCheckpoint reports a replication failure without blocking on abort
// notifications. The bounded mailbox honors caller cancellation; Run applies
// failure policy only if both checkpoint and epoch still match the active one.
func (cc *CheckpointCoordinator) FailCheckpoint(ctx context.Context, checkpointID, epochID uint64, cause error) error {
	if checkpointID == 0 || cause == nil {
		return errors.New("checkpoint failure requires an identity and cause")
	}
	select {
	case cc.failureCh <- checkpointUploadResult{CheckpointID: checkpointID, EpochID: epochID, Err: cause}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run is the main coordinator loop. It listens for timer expiry, ACKs,
// and context cancellation. It should be launched in an errgroup.
func (cc *CheckpointCoordinator) Run(ctx context.Context) error {
	for {
		cc.mu.Lock()
		timer := cc.timer
		checkpointID, epochID := cc.activeCheckpointID, cc.activeEpochID
		cc.mu.Unlock()

		// If no active timer, just wait for ACKs or context cancel.
		var timerCh <-chan time.Time
		if timer != nil {
			timerCh = timer.C
		}

		select {
		case <-ctx.Done():
			cc.mu.Lock()
			if cc.timer != nil {
				cc.timer.Stop()
			}
			cc.mu.Unlock()
			return nil

		case <-cc.triggerCh:
			// New checkpoint triggered — re-read timer at top of loop.
			continue

		case <-timerCh:
			if err := cc.abortCheckpointIdentity(ctx, checkpointID, epochID, nil); err != nil {
				return err
			}

		case failure := <-cc.failureCh:
			if err := cc.abortCheckpointIdentity(ctx, failure.CheckpointID, failure.EpochID, failure.Err); err != nil {
				return err
			}

		case ack := <-cc.ackCh:
			cc.mu.Lock()
			// Ignore stale ACKs (wrong checkpoint ID or no active checkpoint).
			if ack.CheckpointID != cc.activeCheckpointID || cc.activeCheckpointID == 0 || (ack.applied != nil && ack.EpochID != cc.activeEpochID) {
				cc.mu.Unlock()
				if ack.applied != nil {
					close(ack.applied)
				}
				continue
			}

			delete(cc.pendingACKs, ack.TaskIndex)

			if len(cc.pendingACKs) == 0 {
				cc.completeCheckpoint()
			}
			pending := cc.pendingCommit
			cc.pendingCommit = nil
			cc.mu.Unlock()

			if ack.applied != nil {
				close(ack.applied)
			}
			// Send commit notifications outside the lock to avoid deadlock.
			if pending != nil {
				cc.sendCommitNotifications(ctx, pending)
			}
		}
	}
}

// abortCheckpoint sends CtrlAbortCheckpoint to all task slots, increments
// failure counters, and checks thresholds. Must be called without holding mu.
func (cc *CheckpointCoordinator) abortCheckpoint(ctx context.Context) error {
	return cc.abortCheckpointIdentity(ctx, 0, 0, nil)
}

// Zero expected ID is reserved for the existing explicit abort helper.
func (cc *CheckpointCoordinator) abortCheckpointIdentity(ctx context.Context, expectedID, expectedEpoch uint64, cause error) error {
	cc.mu.Lock()
	if cc.activeCheckpointID == 0 || (expectedID != 0 && (expectedID != cc.activeCheckpointID || expectedEpoch != cc.activeEpochID)) {
		// Already completed between timer fire and lock acquisition.
		cc.mu.Unlock()
		return nil
	}

	checkpointID := cc.activeCheckpointID
	epochID := cc.activeEpochID

	// Stop and clean up timer.
	if cc.timer != nil {
		cc.timer.Stop()
		cc.timer = nil
	}

	// Update failure counters.
	cc.consecutiveFailures++
	cc.totalFailures++
	if cause == nil {
		cc.metrics.IncTimeoutTotal()
	}

	cc.log.Warn().Err(cause).
		Uint64("checkpoint_id", checkpointID).
		Int("consecutive_failures", cc.consecutiveFailures).
		Msg("checkpoint failed, aborting")

	// Preserve the terminal error while still releasing task and sink state.
	var failureErr error
	// Check consecutive failure threshold.
	if cc.config.MaxConsecutiveFailures > 0 && cc.consecutiveFailures >= cc.config.MaxConsecutiveFailures {
		failureErr = fmt.Errorf("%w: %d consecutive failures",
			ErrMaxConsecutiveCheckpointFailures, cc.consecutiveFailures)
	}

	// Check tolerable failure rate.
	if failureErr == nil && cc.config.TolerableFailureRate > 0 && cc.totalCheckpoints > 0 {
		rate := float64(cc.totalFailures) / float64(cc.totalCheckpoints)
		if rate > cc.config.TolerableFailureRate {
			failureErr = fmt.Errorf("%w: failure rate %.2f exceeds tolerance %.2f",
				ErrCheckpointFailureRateExceeded, rate, cc.config.TolerableFailureRate)
		}
	}

	// Snapshot transactional sink indices and reset their state while still
	// holding the lock. This avoids a second lock/unlock cycle and prevents
	// a race with RegisterTransactionalSink between the two critical sections.
	sinkIndices := make([]int, 0, len(cc.sinkTxnStates))
	for idx := range cc.sinkTxnStates {
		sinkIndices = append(sinkIndices, idx)
		cc.sinkTxnStates[idx].State = TxnActive
		cc.sinkTxnStates[idx].CurrentCheckpoint = 0
	}

	channels := append([]chan<- ControlMsg(nil), cc.controlChannels...)
	cc.activeCheckpointID = 0
	cc.activeEpochID = 0
	cc.pendingACKs = make(map[int]bool)
	cc.mu.Unlock()

	// Send CtrlAbortTransaction to sink tasks FIRST so they rollback their
	// external transaction before the barrier aligner is reset by
	// CtrlAbortCheckpoint. This prevents a window where the sink processes
	// CtrlAbortCheckpoint (resetting aligner/buffers) but continues writing
	// into an open transaction.
	if len(sinkIndices) > 0 {
		txnAbortMsg := ControlMsg{
			Type:         CtrlAbortTransaction,
			CheckpointID: checkpointID,
			EpochID:      epochID,
		}
		for _, idx := range sinkIndices {
			if idx < len(channels) {
				select {
				case channels[idx] <- txnAbortMsg:
				case <-ctx.Done():
					return errors.Join(failureErr, ctx.Err())
				}
			}
		}
	}

	// Send CtrlAbortCheckpoint to all task slots.
	abortMsg := ControlMsg{
		Type:         CtrlAbortCheckpoint,
		CheckpointID: checkpointID,
		EpochID:      epochID,
	}
	for _, ch := range channels {
		select {
		case ch <- abortMsg:
		case <-ctx.Done():
			return errors.Join(failureErr, ctx.Err())
		}
	}

	return failureErr
}

// completeCheckpoint resets active checkpoint state and consecutive failures.
// Must be called with mu held. If transactional sinks are registered, stores
// pending commit info for deferred notification (sent after releasing mu).
func (cc *CheckpointCoordinator) completeCheckpoint() {
	if cc.activeCheckpointID == 0 {
		return
	}

	checkpointID := cc.activeCheckpointID
	epochID := cc.activeEpochID

	cc.log.Info().
		Uint64("checkpoint_id", checkpointID).
		Msg("checkpoint completed")

	if cc.timer != nil {
		cc.timer.Stop()
		cc.timer = nil
	}

	// Store pending commit info for transactional sinks.
	if len(cc.sinkTxnStates) > 0 {
		cc.pendingCommit = &pendingCommitInfo{
			checkpointID: checkpointID,
			epochID:      epochID,
		}
		for _, state := range cc.sinkTxnStates {
			state.CurrentCheckpoint = checkpointID
			state.State = TxnPreCommitted
		}
	}

	cc.activeCheckpointID = 0
	cc.activeEpochID = 0
	cc.consecutiveFailures = 0
	cc.lastCompletionTime = time.Now()
	cc.lastCompletedCheckpoint = max(cc.lastCompletedCheckpoint, checkpointID)
}

// LastCompletedCheckpoint is the global completion watermark, not the most
// recently triggered or locally aligned checkpoint.
func (cc *CheckpointCoordinator) LastCompletedCheckpoint() uint64 {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.lastCompletedCheckpoint
}

// BindTaskControl connects a TaskSlot's mailbox before that slot starts Run.
func (cc *CheckpointCoordinator) BindTaskControl(index int, ch chan<- ControlMsg) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if index < 0 || index >= len(cc.controlChannels) || ch == nil {
		return errors.New("invalid checkpoint task control binding")
	}
	cc.controlChannels[index] = ch
	return nil
}
