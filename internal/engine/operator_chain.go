package engine

import (
	"context"
	"errors"
	"fmt"
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
	ctx        context.Context
	links      []ChainLink
	inputCh    <-chan Event
	controlCh  <-chan ControlMsg
	outputCh   chan<- OutputMsg
	dlqCh      chan<- DLQEvent // nil if no DLQ configured.
	aligner    *BarrierAligner
	numInputs  int
	cpMetrics  CheckpointMetrics
	errMetrics ErrorMetrics
	log        zerolog.Logger
	txnSink    TransactionalSink
	ackFn      func(checkpointID uint64)
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
	// Panic recovery (safety net for non-operator panics).
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("%w: %v", ErrOperatorPanic, r)
			log.Error().Interface("panic", r).Msg("operator chain panic")
		}
	}()

	// Open all operators.
	for i, op := range operators {
		if err := op.Open(ctx); err != nil {
			// Close already-opened operators in reverse order.
			for j := i - 1; j >= 0; j-- {
				_ = operators[j].Close()
			}
			return fmt.Errorf("operator[%d] open: %w", i, err)
		}
	}

	// Close all operators in reverse order on exit.
	defer func() {
		for i := len(operators) - 1; i >= 0; i-- {
			if err := operators[i].Close(); err != nil {
				log.Warn().Err(err).Int("operator", i).Msg("operator close error")
			}
		}
	}()

	// If the last operator is a TransactionalSink, begin the initial transaction.
	if txnSink != nil {
		if err := txnSink.BeginTransaction(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}
	}

	// Build chain links (pairs operators with error configs).
	links := buildChainLinks(operators, errorConfigs)

	// Resolve error metrics.
	if errMetrics == nil {
		errMetrics = NoopErrorMetrics()
	}

	cc := &chainContext{
		ctx:        ctx,
		links:      links,
		inputCh:    inputCh,
		controlCh:  controlCh,
		outputCh:   outputCh,
		dlqCh:      dlqCh,
		aligner:    aligner,
		numInputs:  numInputs,
		cpMetrics:  metrics,
		errMetrics: errMetrics,
		log:        log,
		txnSink:    txnSink,
		ackFn:      ackFn,
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

		// Phase 2: Blocking select on both channels.
		select {
		case <-ctx.Done():
			return ctx.Err()
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
		case event, ok := <-inputCh:
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
//
// Fast path: while no FlatMap fan-out has been seen, the current event is
// tracked inline (cur + alive) without any per-event slice allocation.
// On the first FlatMap with >1 emission, we promote to a slice and use
// the original fan-out logic for the remaining links.
func processEvent(cc *chainContext, event Event) error {
	var (
		cur    Event   = event
		alive          = true
		events []Event // promoted on FlatMap fan-out only
	)

	for _, link := range cc.links {
		if events == nil {
			// Single-event fast path.
			if !alive {
				return nil
			}
			switch o := link.Operator.(type) {
			case MapOperator:
				result, err := invokeMapWithRetry(cc, link, cur, o)
				if err != nil {
					return err
				}
				if result == nil {
					alive = false
					continue
				}
				cur = *result
			case FlatMapOperator:
				emitted, err := invokeFlatMapWithRetry(cc, link, cur, o)
				if err != nil {
					return err
				}
				switch len(emitted) {
				case 0:
					alive = false
				case 1:
					cur = emitted[0]
				default:
					// Promote to slice path for the remaining links.
					events = emitted
				}
			case SinkOperator:
				if err := invokeSinkWithRetry(cc, link, cur, o); err != nil {
					return err
				}
				// Sink consumes the event; no further links should run.
				alive = false
			}
			continue
		}

		// Slice path (post fan-out).
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

	// Send surviving event(s) to output.
	if events == nil {
		if !alive {
			return nil
		}
		select {
		case cc.outputCh <- OutputMsg{Type: OutputData, Event: cur}:
		case <-cc.ctx.Done():
			return cc.ctx.Err()
		}
		return nil
	}
	for _, e := range events {
		select {
		case cc.outputCh <- OutputMsg{Type: OutputData, Event: e}:
		case <-cc.ctx.Done():
			return cc.ctx.Err()
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
		return nil
	}

	return invokeWithRetry(cc, link, e, func() error {
		return op.Write(cc.ctx, e)
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
	switch ctrl.Type {
	case CtrlBarrierReceived:
		if !cc.aligner.AllAligned(ctrl.CheckpointID) {
			return nil // Not all inputs aligned yet.
		}

		cc.log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("all barriers aligned, checkpointing")

		// Record alignment time metric.
		if startTime := cc.aligner.AlignmentStartTime(); !startTime.IsZero() {
			cc.cpMetrics.ObserveAlignmentTime(time.Since(startTime))
		}

		// Snapshot all operators.
		for i, link := range cc.links {
			if _, err := link.Operator.Checkpoint(ctrl.CheckpointID); err != nil {
				return fmt.Errorf("operator[%d] checkpoint: %w", i, err)
			}
		}

		// Drain side-buffered events and process them inline.
		drained := cc.aligner.DrainAll(ctrl.CheckpointID)
		for _, event := range drained {
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}

		if cc.txnSink != nil {
			// Transactional sink: PreCommit and ACK to coordinator.
			// Do NOT forward barrier downstream (sink is terminal).
			if err := cc.txnSink.PreCommit(cc.ctx, ctrl.CheckpointID); err != nil {
				return fmt.Errorf("%w: %v", ErrPreCommitFailed, err)
			}
			if cc.ackFn != nil {
				cc.ackFn(ctrl.CheckpointID)
			}
		} else {
			// Non-transactional: forward barrier downstream (existing behavior).
			barrier := &protocol.CheckpointBarrierMsg{
				CheckpointID: ctrl.CheckpointID,
				EpochID:      ctrl.EpochID,
				Timestamp:    time.Now().UnixMilli(),
			}
			select {
			case cc.outputCh <- OutputMsg{Type: OutputBarrier, Barrier: barrier}:
			case <-cc.ctx.Done():
				return cc.ctx.Err()
			}
		}

		cc.aligner.Reset(ctrl.CheckpointID)

	case CtrlCommitCheckpoint:
		if cc.txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		cc.log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("committing transaction")
		if err := cc.txnSink.Commit(cc.ctx, ctrl.CheckpointID); err != nil {
			return fmt.Errorf("%w: %v", ErrCommitFailed, err)
		}
		if err := cc.txnSink.BeginTransaction(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortTransaction:
		if cc.txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		cc.log.Warn().Msg("aborting transaction")
		if err := cc.txnSink.Abort(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrAbortFailed, err)
		}
		if err := cc.txnSink.BeginTransaction(cc.ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortCheckpoint:
		cc.log.Warn().Uint64("checkpoint", ctrl.CheckpointID).Msg("aborting checkpoint")
		// Drain side buffers and process them (events are still valid, just no checkpoint).
		drained := cc.aligner.DrainAll(ctrl.CheckpointID)
		for _, event := range drained {
			if err := processEvent(cc, event); err != nil {
				return err
			}
		}
		cc.aligner.Reset(ctrl.CheckpointID)

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

			cc.log.Info().Msg("all inputs exhausted, forwarding EndOfPartition")
			eop := &protocol.EndOfPartitionMsg{
				Reason: protocol.EndReasonExhausted,
			}
			select {
			case cc.outputCh <- OutputMsg{Type: OutputEnd, End: eop}:
			case <-cc.ctx.Done():
				return cc.ctx.Err()
			}
			return errChainDone
		}

	case CtrlShutdown:
		cc.log.Info().Msg("shutdown control received")
		return errChainDone
	}

	return nil
}
