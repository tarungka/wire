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
) (retErr error) {
	// Panic recovery.
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
				operators[j].Close()
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
				if err := handleControl(ctx, ctrl, operators, aligner, inputCh, outputCh, numInputs, &eofCount, metrics, log, txnSink, ackFn); err != nil {
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
			if err := handleControl(ctx, ctrl, operators, aligner, inputCh, outputCh, numInputs, &eofCount, metrics, log, txnSink, ackFn); err != nil {
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
			if err := processEvent(ctx, event, operators, outputCh); err != nil {
				return err
			}
		}
	}
}

// processEvent runs a single event through the fused operator chain.
// MapOperator: one-to-one. FlatMapOperator: one-to-many. SinkOperator: terminal.
func processEvent(ctx context.Context, event Event, operators []Operator, outputCh chan<- OutputMsg) error {
	// Start with the input event. For FlatMap we may fan out to multiple events.
	events := []Event{event}

	for _, op := range operators {
		var next []Event
		for _, e := range events {
			switch o := op.(type) {
			case MapOperator:
				result, err := o.Map(ctx, e)
				if err != nil {
					return fmt.Errorf("map operator: %w", err)
				}
				// Zero-value Event (empty Key, Value, and EventTime 0) means filtered.
				if result.Key != nil || result.Value != nil || result.EventTime != 0 || result.Headers != nil {
					next = append(next, result)
				}
			case FlatMapOperator:
				err := o.FlatMap(ctx, e, func(emitted Event) {
					next = append(next, emitted)
				})
				if err != nil {
					return fmt.Errorf("flatmap operator: %w", err)
				}
			case SinkOperator:
				if err := o.Write(ctx, e); err != nil {
					return fmt.Errorf("sink operator: %w", err)
				}
				// Sink is terminal — no further output.
				return nil
			}
		}
		events = next
	}

	// Send surviving events to output.
	for _, e := range events {
		select {
		case outputCh <- OutputMsg{Type: OutputData, Event: e}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// drainInputCh non-blocking-drains all remaining events from inputCh and
// processes them through the operator chain.
func drainInputCh(ctx context.Context, inputCh <-chan Event, operators []Operator, outputCh chan<- OutputMsg) error {
	for {
		select {
		case event, ok := <-inputCh:
			if !ok {
				return nil
			}
			if err := processEvent(ctx, event, operators, outputCh); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// handleControl processes a control message.
func handleControl(
	ctx context.Context,
	ctrl ControlMsg,
	operators []Operator,
	aligner *BarrierAligner,
	inputCh <-chan Event,
	outputCh chan<- OutputMsg,
	numInputs int,
	eofCount *int,
	metrics CheckpointMetrics,
	log zerolog.Logger,
	txnSink TransactionalSink,
	ackFn func(checkpointID uint64),
) error {
	switch ctrl.Type {
	case CtrlBarrierReceived:
		if !aligner.AllAligned(ctrl.CheckpointID) {
			return nil // Not all inputs aligned yet.
		}

		log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("all barriers aligned, checkpointing")

		// Record alignment time metric.
		if startTime := aligner.AlignmentStartTime(); !startTime.IsZero() {
			metrics.ObserveAlignmentTime(time.Since(startTime))
		}

		// Snapshot all operators.
		for i, op := range operators {
			if _, err := op.Checkpoint(ctrl.CheckpointID); err != nil {
				return fmt.Errorf("operator[%d] checkpoint: %w", i, err)
			}
		}

		// Drain side-buffered events and process them inline.
		drained := aligner.DrainAll(ctrl.CheckpointID)
		for _, event := range drained {
			if err := processEvent(ctx, event, operators, outputCh); err != nil {
				return err
			}
		}

		if txnSink != nil {
			// Transactional sink: PreCommit and ACK to coordinator.
			// Do NOT forward barrier downstream (sink is terminal).
			if err := txnSink.PreCommit(ctx, ctrl.CheckpointID); err != nil {
				return fmt.Errorf("%w: %v", ErrPreCommitFailed, err)
			}
			if ackFn != nil {
				ackFn(ctrl.CheckpointID)
			}
		} else {
			// Non-transactional: forward barrier downstream (existing behavior).
			barrier := &protocol.CheckpointBarrierMsg{
				CheckpointID: ctrl.CheckpointID,
				EpochID:      ctrl.EpochID,
				Timestamp:    time.Now().UnixMilli(),
			}
			select {
			case outputCh <- OutputMsg{Type: OutputBarrier, Barrier: barrier}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		aligner.Reset(ctrl.CheckpointID)

	case CtrlCommitCheckpoint:
		if txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		log.Debug().Uint64("checkpoint", ctrl.CheckpointID).Msg("committing transaction")
		if err := txnSink.Commit(ctx, ctrl.CheckpointID); err != nil {
			return fmt.Errorf("%w: %v", ErrCommitFailed, err)
		}
		if err := txnSink.BeginTransaction(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortTransaction:
		if txnSink == nil {
			break // Ignore for non-transactional sinks.
		}
		log.Warn().Msg("aborting transaction")
		if err := txnSink.Abort(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrAbortFailed, err)
		}
		if err := txnSink.BeginTransaction(ctx); err != nil {
			return fmt.Errorf("%w: %v", ErrBeginTransactionFailed, err)
		}

	case CtrlAbortCheckpoint:
		log.Warn().Uint64("checkpoint", ctrl.CheckpointID).Msg("aborting checkpoint")
		// Drain side buffers and process them (events are still valid, just no checkpoint).
		drained := aligner.DrainAll(ctrl.CheckpointID)
		for _, event := range drained {
			if err := processEvent(ctx, event, operators, outputCh); err != nil {
				return err
			}
		}
		aligner.Reset(ctrl.CheckpointID)

	case CtrlEndOfPartition:
		*eofCount++
		log.Debug().Int("input", ctrl.InputIndex).Int("eof_count", *eofCount).Int("num_inputs", numInputs).Msg("input EOF")
		if *eofCount >= numInputs {
			// Drain any remaining events in the input channel before
			// forwarding EoP downstream. This ensures all events sent before
			// the EoP control message are processed.
			if err := drainInputCh(ctx, inputCh, operators, outputCh); err != nil {
				return err
			}

			log.Info().Msg("all inputs exhausted, forwarding EndOfPartition")
			eop := &protocol.EndOfPartitionMsg{
				Reason: protocol.EndReasonExhausted,
			}
			select {
			case outputCh <- OutputMsg{Type: OutputEnd, End: eop}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return errChainDone
		}

	case CtrlShutdown:
		log.Info().Msg("shutdown control received")
		return errChainDone
	}

	return nil
}
