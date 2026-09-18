package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
)

// errChainDone is a sentinel returned by handleControl to signal the operator
// chain should exit cleanly. It is never surfaced to the caller.
var errChainDone = errors.New("operator chain done")

// chainContext consolidates parameters passed between the operator chain
// functions, avoiding long parameter lists.
type chainContext struct {
	checkpoint                 *chainCheckpointState
	draining                   bool
	preparedCheckpoint         uint64
	transactionPrepared        bool
	transactionDecisionPending bool
	lastCommitted              uint64
	lastAborted                checkpointIdentity
	deferredEOF                []ControlMsg
	deferredEvents             []Event
	ctx                        context.Context
	links                      []ChainLink
	inputCh                    <-chan Event
	controlCh                  <-chan ControlMsg
	outputCh                   chan<- OutputMsg
	dlqCh                      chan<- DLQEvent // nil if no DLQ configured.
	aligner                    *BarrierAligner
	numInputs                  int
	cpMetrics                  CheckpointMetrics
	errMetrics                 ErrorMetrics
	log                        zerolog.Logger
	txnSink                    TransactionalSink
	ackFn                      func(checkpointID uint64)
}

// runOperatorChain is the main processing goroutine. It reads events from
// inputCh and control messages from controlCh, runs events through the fused
// operator chain, and sends results to outputCh.
//
// Design points:
//   - Panic recovery around all operator calls
//   - Two-phase select for control mailbox priority
//   - Opens operators at start, closes in reverse order on exit
//   - DrainAll events processed inline (not re-injected into inputCh)
//   - TransactionalSink detection: if last operator implements TransactionalSink,
//     BeginTransaction is called at startup and 2PC protocol is used for checkpoints
//   - Per-operator error handling: retry transient errors, route poison messages
//     to DLQ, fail the job on fatal errors (WIP-11)
func runOperatorChain(
	ctx context.Context,
	operators []Operator,
	inputCh <-chan Event,
	controlCh <-chan ControlMsg,
	outputCh chan<- OutputMsg,
	aligner *BarrierAligner,
	numInputs int,
	metrics CheckpointMetrics,
	log zerolog.Logger,
	txnSink TransactionalSink,
	ackFn func(checkpointID uint64),
	errorConfigs []ErrorHandlerConfig,
	dlqCh chan<- DLQEvent,
	errMetrics ErrorMetrics,
) (retErr error) {
	closeOperators, err := openOperators(ctx, operators, log)
	if err != nil {
		return err
	}
	defer closeOperators()
	return runOpenedOperatorChain(ctx, operators, inputCh, controlCh, outputCh, aligner, numInputs, metrics, log, txnSink, ackFn, errorConfigs, dlqCh, errMetrics, 0)
}

// runOpenedOperatorChain processes an already-opened chain. Its caller owns
// initialization and cleanup, including waiting for source readers to exit.
func runOpenedOperatorChain(
	ctx context.Context,
	operators []Operator,
	inputCh <-chan Event,
	controlCh <-chan ControlMsg,
	outputCh chan<- OutputMsg,
	aligner *BarrierAligner,
	numInputs int,
	metrics CheckpointMetrics,
	log zerolog.Logger,
	txnSink TransactionalSink,
	ackFn func(checkpointID uint64),
	errorConfigs []ErrorHandlerConfig,
	dlqCh chan<- DLQEvent,
	errMetrics ErrorMetrics,
	restoredCommitted uint64,
	checkpoint ...*chainCheckpointState,
) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = &OperatorPanicError{Value: r, Stack: string(debug.Stack())}
			log.Error().Interface("panic", r).Msg("operator chain panic")
		}
	}()

	var cc *chainContext
	// If the last operator is a TransactionalSink, begin the initial transaction.
	if txnSink != nil {
		if err := txnSink.BeginTransaction(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}
		// Only an unreported transaction is safe to abort locally. Once an ACK
		// may have reached the coordinator (or commit was attempted), recovery
		// must resolve the durable decision; cancellation is not an abort vote.
		defer func() {
			if cc != nil && cc.transactionDecisionPending {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := safeInvoke(func() error { return txnSink.Abort(cleanupCtx) }); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("%w: %w", ErrAbortFailed, err))
			}
		}()
	}

	// Build chain links (pairs operators with error configs).
	links := buildChainLinks(operators, errorConfigs)

	// Resolve error metrics.
	if errMetrics == nil {
		errMetrics = NoopErrorMetrics()
	}

	cc = &chainContext{
		lastCommitted: restoredCommitted,
		ctx:           ctx,
		links:         links,
		inputCh:       inputCh,
		controlCh:     controlCh,
		outputCh:      outputCh,
		dlqCh:         dlqCh,
		aligner:       aligner,
		numInputs:     numInputs,
		cpMetrics:     metrics,
		errMetrics:    errMetrics,
		log:           log,
		txnSink:       txnSink,
		ackFn:         ackFn,
	}

	if len(checkpoint) > 0 {
		cc.checkpoint = checkpoint[0]
	}
	var uploadResults <-chan checkpointUploadResult
	if cc.checkpoint != nil {
		uploadResults = cc.checkpoint.uploader.results
	}
	eofCount := 0

	for {
		// Phase 1: Non-blocking drain of controlCh (priority).
		drained := true
		for drained {
			select {
			case ctrl, ok := <-controlCh:
				if !ok {
					return nil
				}
				if err := handleControl(cc, ctrl, &eofCount); err != nil {
					if err == errChainDone {
						return nil
					}
					return err
				}
			default:
				drained = false
			}
		}

		if cc.checkpoint != nil && cc.checkpoint.endPending && len(cc.checkpoint.pending) == 0 {
			return emitChainEnd(cc)
		}

		// A prepared transaction cannot accept post-barrier records until
		// its coordinator decision arrives. Keep consuming control messages.
		events := inputCh
		if cc.transactionPrepared {
			events = nil
		}
		// Phase 2: Blocking select on both channels.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-uploadResults:
			if err := cc.checkpoint.complete(ctx, result); err != nil {
				return err
			}
			if cc.checkpoint.endPending && len(cc.checkpoint.pending) == 0 {
				return emitChainEnd(cc)
			}
		case ctrl, ok := <-controlCh:
			if !ok {
				return nil
			}
			if err := handleControl(cc, ctrl, &eofCount); err != nil {
				if err == errChainDone {
					return nil
				}
				return err
			}
		case event, ok := <-events:
			if !ok {
				log.Debug().Msg("input channel closed")
				return nil
			}
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}
	}
}

// isZeroEvent returns true if the event is a zero-value (filtered) event.
func isZeroEvent(e Event) bool {
	return e.Key == nil && e.Value == nil && e.EventTime == 0 && e.Headers == nil
}

// processEvent runs a single event through the fused operator chain.
// MapOperator: one-to-one. FlatMapOperator: one-to-many. SinkOperator: terminal.
// When an operator has a non-zero ErrorHandlerConfig, errors are handled via
// invokeWithRetry (retry/DLQ/drop); otherwise errors fail the job immediately.
func processEvent(cc *chainContext, event Event) error {
	if activity := event.inputActivity; activity != nil {
		defer activity.tracker.recordProcessed(activity.input)
		event.inputActivity = nil
	}
	if boundary := event.inputWatermark; boundary != nil {
		boundary.tracker.AdvanceWatermark(boundary.input, boundary.timestamp)
		return nil
	}
	if event.watermark != nil {
		return processWatermark(cc, *event.watermark)
	}
	recordTaskInput(cc.ctx, event)
	return processEventFrom(cc, event, cc.links)
}

func processEventFrom(cc *chainContext, event Event, links []ChainLink) error {
	// Start with the input event. For FlatMap we may fan out to multiple events.
	events := []Event{event}

	for _, link := range links {
		var next []Event
		for _, e := range events {
			switch o := link.Operator.(type) {
			case MapOperator:
				result, err := invokeMapWithRetry(cc, link, e, o)
				if err != nil {
					return err
				}
				if result != nil {
					next = append(next, *result)
				}
			case FlatMapOperator:
				emitted, err := invokeFlatMapWithRetry(cc, link, e, o)
				if err != nil {
					return err
				}
				next = append(next, emitted...)
			case SinkOperator:
				if err := invokeSinkWithRetry(cc, link, e, o); err != nil {
					return err
				}
			}
		}
		events = next
	}

	// Send surviving events to output.
	for _, e := range events {
		if err := cc.sendOutput(OutputMsg{Type: OutputData, Event: e}); err != nil {
			return err
		}
	}
	return nil
}

// invokeMapWithRetry wraps a MapOperator call with error handling.
// Returns nil Event pointer if the event was filtered or DLQ'd/dropped.
func invokeMapWithRetry(cc *chainContext, link ChainLink, e Event, op MapOperator) (*Event, error) {
	hasErrorHandling := link.Config.MaxRetries > 0 || link.Config.OnExhausted != FailJob || link.Config.Classifier != nil

	if !hasErrorHandling {
		// Legacy path: no error handling, fail on any error.
		result, err := op.Map(cc.ctx, e)
		if err != nil {
			return nil, fmt.Errorf("map operator: %w", err)
		}
		if isZeroEvent(result) {
			return nil, nil // Filtered.
		}
		return &result, nil
	}

	var result Event
	err := invokeWithRetry(cc, link, e, func() error {
		var mapErr error
		result, mapErr = op.Map(cc.ctx, e)
		return mapErr
	})
	if err != nil {
		return nil, err
	}
	if isZeroEvent(result) {
		return nil, nil // Filtered or DLQ'd.
	}
	return &result, nil
}

// invokeFlatMapWithRetry wraps a FlatMapOperator call with error handling.
func invokeFlatMapWithRetry(cc *chainContext, link ChainLink, e Event, op FlatMapOperator) ([]Event, error) {
	hasErrorHandling := link.Config.MaxRetries > 0 || link.Config.OnExhausted != FailJob || link.Config.Classifier != nil

	if !hasErrorHandling {
		// Legacy path.
		var emitted []Event
		err := op.FlatMap(cc.ctx, e, func(out Event) {
			emitted = append(emitted, out)
		})
		if err != nil {
			return nil, fmt.Errorf("flatmap operator: %w", err)
		}
		return emitted, nil
	}

	var emitted []Event
	err := invokeWithRetry(cc, link, e, func() error {
		emitted = nil // Reset on retry.
		return op.FlatMap(cc.ctx, e, func(out Event) {
			emitted = append(emitted, out)
		})
	})
	if err != nil {
		return nil, err
	}
	return emitted, nil
}

// invokeSinkWithRetry wraps a SinkOperator Write call with error handling.
func invokeSinkWithRetry(cc *chainContext, link ChainLink, e Event, op SinkOperator) error {
	hasErrorHandling := link.Config.MaxRetries > 0 || link.Config.OnExhausted != FailJob || link.Config.Classifier != nil

	if !hasErrorHandling {
		// Legacy path.
		if err := op.Write(cc.ctx, e); err != nil {
			return fmt.Errorf("sink operator: %w", err)
		}
		recordTaskOutput(cc.ctx, e)
		return nil
	}

	return invokeWithRetry(cc, link, e, func() error {
		err := op.Write(cc.ctx, e)
		if err == nil {
			recordTaskOutput(cc.ctx, e)
		}
		return err
	})
}

// drainInputCh non-blocking-drains all remaining events from inputCh and
// processes them through the operator chain.
func drainInputCh(cc *chainContext) error {
	for {
		select {
		case event, ok := <-cc.inputCh:
			if !ok {
				return nil
			}
			if err := processEvent(cc, event); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// handleControl processes a control message.
func handleControl(cc *chainContext, ctrl ControlMsg, eofCount *int) error {
	if ctrl.sourceBoundary != nil {
		defer close(ctrl.sourceBoundary.done)
	}
	if cc.checkpoint != nil && ctrl.Type == CtrlAbortCheckpoint {
		cc.checkpoint.abort(ctrl.CheckpointID, ctrl.EpochID)
	}
	if cc.draining && ctrl.Type == CtrlBarrierReceived {
		return nil
	}

	if cc.transactionPrepared {
		switch ctrl.Type {
		case CtrlEndOfPartition:
			if len(cc.deferredEOF) >= cc.numInputs {
				return fmt.Errorf("too many EOF notifications while transaction prepared")
			}
			cc.deferredEOF = append(cc.deferredEOF, ctrl)
			return nil
		case CtrlBarrierReceived:
			if ctrl.CheckpointID == cc.preparedCheckpoint {
				return nil
			}
			return fmt.Errorf("checkpoint %d arrived while transaction %d is prepared", ctrl.CheckpointID, cc.preparedCheckpoint)
		case CtrlAbortCheckpoint:
			if ctrl.CheckpointID != cc.preparedCheckpoint {
				return nil
			}
			if err := handleControl(cc, ControlMsg{Type: CtrlAbortTransaction, CheckpointID: ctrl.CheckpointID, EpochID: ctrl.EpochID}, eofCount); err != nil {
				return err
			}
		}
	}

	switch ctrl.Type {
	case CtrlBarrierReceived:
		if !cc.aligner.AllAligned(ctrl.CheckpointID) || cc.aligner.ActiveEpochID() != ctrl.EpochID {
			return nil // Not all inputs aligned yet.
		}

		cc.log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("all barriers aligned, checkpointing")

		// Record alignment time metric.
		if startTime := cc.aligner.AlignmentStartTime(); !startTime.IsZero() {
			cc.cpMetrics.ObserveAlignmentTime(time.Since(startTime))
		}

		// All readers enqueued their pre-barrier records before marking alignment.
		// Consume them before snapshotting, despite control-channel priority.
		if err := drainInputCh(cc); err != nil {
			return err
		}

		// Prepare first so the snapshot can contain the durable transaction
		// handle needed to repeat Commit after a worker restart.
		if cc.txnSink != nil {
			if cc.transactionPrepared {
				return fmt.Errorf("transaction already prepared for checkpoint %d", cc.preparedCheckpoint)
			}
			// Transactional sink: PreCommit and ACK to coordinator.
			// Do NOT forward barrier downstream (sink is terminal).
			if err := cc.txnSink.PreCommit(cc.ctx, ctrl.CheckpointID); err != nil {
				return fmt.Errorf("%w: %v", ErrPreCommitFailed, err)
			}
			cc.preparedCheckpoint = ctrl.CheckpointID
			cc.transactionPrepared = true
		}

		// Capture snapshot bytes synchronously at the aligned boundary.
		var snapshots [][]byte
		var stateHandleIndexes []int
		if cc.checkpoint != nil {
			snapshots = make([][]byte, len(cc.links))
		}
		for i, link := range cc.links {
			data, typed, err := captureOperatorCheckpoint(link.Operator, ctrl.CheckpointID)
			if err != nil {
				return fmt.Errorf("operator[%d] checkpoint: %w", i, err)
			}
			if snapshots != nil {
				snapshots[i] = append([]byte(nil), data...)
				if typed {
					stateHandleIndexes = append(stateHandleIndexes, i)
				}
			}
		}

		if cc.txnSink != nil {
			if cc.ackFn != nil && cc.checkpoint == nil {
				cc.transactionDecisionPending = true
				cc.ackFn(ctrl.CheckpointID)
			}
		} else {
			// Non-transactional: forward barrier downstream (existing behavior).
			barrier := &protocol.CheckpointBarrierMsg{
				CheckpointID: ctrl.CheckpointID,
				EpochID:      ctrl.EpochID,
				Timestamp:    time.Now().UnixMilli(),
			}
			if err := cc.sendOutput(OutputMsg{Type: OutputBarrier, Barrier: barrier}); err != nil {
				return err
			}
		}

		if cc.checkpoint != nil {
			// Upload/report is asynchronous. Preserve prepared state even if
			// cancellation races a successful report and its commit decision.
			cc.transactionDecisionPending = cc.transactionPrepared
			if err := cc.checkpoint.submit(cc.ctx, ctrl.CheckpointID, ctrl.EpochID, snapshots, stateHandleIndexes, cc.transactionPrepared, cc.lastCommitted, ctrl.sourceBoundary); err != nil {
				return err
			}
		}

		// Publish the barrier before releasing its post-barrier records.
		drained := cc.aligner.FinishAlignment(ctrl.CheckpointID)
		if cc.txnSink != nil {
			cc.deferredEvents = drained
		} else {
			for _, event := range drained {
				if err := processEvent(cc, event); err != nil {
					return err
				}
			}
		}

	case CtrlCommitCheckpoint:
		if cc.txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		if ctrl.CheckpointID <= cc.lastCommitted {
			break
		}
		if !cc.transactionPrepared || ctrl.CheckpointID != cc.preparedCheckpoint {
			return fmt.Errorf("commit checkpoint %d does not match prepared transaction", ctrl.CheckpointID)
		}
		cc.log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("committing transaction")
		cc.transactionDecisionPending = true
		if err := commitTransaction(cc.ctx, cc.txnSink, ctrl.CheckpointID); err != nil {
			return err
		}
		cc.lastCommitted = ctrl.CheckpointID
		cc.transactionPrepared = false
		cc.transactionDecisionPending = false
		if err := cc.txnSink.BeginTransaction(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortTransaction:
		if ctrl.CheckpointID != 0 && (ctrl.EpochID < cc.lastAborted.epoch || (ctrl.EpochID == cc.lastAborted.epoch && ctrl.CheckpointID <= cc.lastAborted.id)) {
			break
		}
		if cc.txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		if ctrl.CheckpointID != 0 && ctrl.CheckpointID <= cc.lastCommitted {
			break
		}
		if cc.transactionPrepared && ctrl.CheckpointID != 0 && ctrl.CheckpointID != cc.preparedCheckpoint {
			return fmt.Errorf("abort checkpoint does not match prepared transaction")
		}
		cc.log.Warn().Msg("aborting transaction")
		if err := cc.txnSink.Abort(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrAbortFailed, err)
		}
		cc.transactionPrepared = false
		cc.transactionDecisionPending = false
		cc.lastAborted = checkpointIdentity{ctrl.CheckpointID, ctrl.EpochID}
		if err := cc.txnSink.BeginTransaction(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortCheckpoint:
		cc.log.Warn().Uint64("checkpoint", ctrl.CheckpointID).Msg("aborting checkpoint")
		if cc.checkpoint != nil {
			cc.checkpoint.abort(ctrl.CheckpointID, ctrl.EpochID)
		}
		// Abort controls have priority, but pre-barrier queued records must
		// still precede the post-barrier side buffer. Bound this drain so an
		// unaligned input cannot extend the drain indefinitely. Each processEvent
		// can still block on downstream progress; this is not a time bound.
	abortDrain:
		for remaining := len(cc.inputCh); remaining > 0; remaining-- {
			select {
			case event, ok := <-cc.inputCh:
				if !ok {
					break abortDrain
				}
				if err := processEvent(cc, event); err != nil {
					return err
				}
			case <-cc.ctx.Done():
				return cc.ctx.Err()
			}
		}
		if start := cc.aligner.AlignmentStartTime(); !start.IsZero() && cc.aligner.ActiveCheckpointID() == ctrl.CheckpointID && cc.aligner.ActiveEpochID() == ctrl.EpochID {
			cc.cpMetrics.ObserveAlignmentTime(time.Since(start))
		}
		drained := cc.aligner.AbortAlignment(ctrl.CheckpointID, ctrl.EpochID)
		for _, event := range drained {
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}

	case CtrlEndOfPartition:
		*eofCount++
		cc.log.Debug().Int("input", ctrl.InputIndex).Int("eof_count", *eofCount).Int("num_inputs", cc.numInputs).Msg("input EOF")
		if *eofCount >= cc.numInputs {
			// Drain any remaining events in the input channel before
			// forwarding EoP downstream. This ensures all events sent before
			// the EoP control message are processed.
			if err := drainInputCh(cc); err != nil {
				return err
			}

			if cc.checkpoint != nil && len(cc.checkpoint.pending) > 0 {
				cc.checkpoint.endPending = true
				return nil
			}
			if err := emitChainEnd(cc); err != nil {
				return err
			}
			return errChainDone
		}

	case CtrlDrainInputs:
		cc.draining = true
		if cc.transactionPrepared {
			// Prepared state cannot accept additional writes without a
			// coordinator decision. Shutdown aborts it through normal cleanup.
			return errChainDone
		}
		if err := drainInputCh(cc); err != nil {
			return err
		}
		for _, event := range cc.aligner.BeginDrain() {
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}
	case CtrlShutdown:
		if cc.draining && !cc.transactionPrepared {
			if err := drainInputCh(cc); err != nil {
				return err
			}
		}
		cc.log.Info().Msg("shutdown control received")
		return errChainDone
	}

	if !cc.transactionPrepared && len(cc.deferredEvents) > 0 {
		pending := cc.deferredEvents
		cc.deferredEvents = nil
		for _, event := range pending {
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}
	}
	if !cc.transactionPrepared && len(cc.deferredEOF) > 0 {
		pending := cc.deferredEOF
		cc.deferredEOF = nil
		for _, eof := range pending {
			if err := handleControl(cc, eof, eofCount); err != nil {
				return err
			}
		}
	}

	return nil
}

func emitChainEnd(cc *chainContext) error {
	return cc.sendOutput(OutputMsg{Type: OutputEnd, End: &protocol.EndOfPartitionMsg{Reason: protocol.EndReasonExhausted}})
}
