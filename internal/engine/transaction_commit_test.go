package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retryCommitSink struct {
	mockTransactionalSink
	calls    int
	ids      []uint64
	failures int
	cancel   context.CancelFunc
}

func (s *retryCommitSink) Commit(_ context.Context, id uint64) error {
	s.calls++
	s.ids = append(s.ids, id)
	if s.cancel != nil {
		s.cancel()
	}
	if s.calls <= s.failures {
		return errTestCommitUnavailable
	}
	return nil
}

var errTestCommitUnavailable = errors.New("external transaction service unavailable")

func TestCommitRetriesSameDecision(t *testing.T) {
	sink := &retryCommitSink{failures: 2}
	if err := retryTransactionCommit(t.Context(), sink, 42, 3, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 3 || sink.BeginTxnCalls() != 0 || sink.AbortCallCount() != 0 {
		t.Fatalf("retry changed transaction: calls=%d", sink.calls)
	}
	for _, id := range sink.ids {
		if id != 42 {
			t.Fatalf("retry changed decision: %v", sink.ids)
		}
	}
}

func TestCommitRetryExhaustionPreservesCause(t *testing.T) {
	sink := &retryCommitSink{failures: 10}
	err := retryTransactionCommit(t.Context(), sink, 7, 3, time.Millisecond)
	if !errors.Is(err, ErrCommitFailed) || !errors.Is(err, errTestCommitUnavailable) || sink.calls != 3 {
		t.Fatalf("calls=%d error=%v", sink.calls, err)
	}
}

func TestCommitRetryCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &retryCommitSink{failures: 10, cancel: cancel}
	started := time.Now()
	err := retryTransactionCommit(ctx, sink, 7, 5, time.Hour)
	if !errors.Is(err, context.Canceled) || sink.calls != 1 || time.Since(started) > time.Second {
		t.Fatalf("cancellation did not stop retries: calls=%d err=%v", sink.calls, err)
	}
}

func TestTransactionCleanupDoesNotAbortAfterACK(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &mockTransactionalSink{}
	aligner := NewBarrierAligner(1, 10)
	aligner.OnBarrier(0, 7, 1)
	controls := make(chan ControlMsg, 1)
	controls <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 7, EpochID: 1}
	err := runOperatorChain(ctx, []Operator{sink}, make(chan Event), controls, make(chan OutputMsg), aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, func(uint64) { cancel() }, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, context.Canceled) || sink.AbortCallCount() != 0 {
		t.Fatalf("uncertain decision was aborted: err=%v aborts=%d", err, sink.AbortCallCount())
	}
}

func TestTransactionCleanupDoesNotAbortFailedCommit(t *testing.T) {
	sink := &mockTransactionalSink{commitErr: errTestCommitUnavailable}
	aligner := NewBarrierAligner(1, 10)
	aligner.OnBarrier(0, 7, 1)
	controls := make(chan ControlMsg, 2)
	controls <- ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 7, EpochID: 1}
	controls <- ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 7, EpochID: 1}
	err := runOperatorChain(t.Context(), []Operator{sink}, make(chan Event), controls, make(chan OutputMsg), aligner, 1, NoopCheckpointMetrics(), testLogger(), sink, nil, nil, nil, NoopErrorMetrics())
	if !errors.Is(err, ErrCommitFailed) || sink.AbortCallCount() != 0 || len(sink.CommitCallIDs()) != transactionCommitAttempts {
		t.Fatalf("durable commit decision was rolled back: err=%v aborts=%d commits=%v", err, sink.AbortCallCount(), sink.CommitCallIDs())
	}
}

type restoredTransactionSink struct {
	retryCommitSink
	restored bool
}

func (s *restoredTransactionSink) RestoreCheckpoint(data []byte) error {
	s.restored = string(data) == "prepared-handle"
	return nil
}
func (s *restoredTransactionSink) Commit(ctx context.Context, id uint64) error {
	if !s.restored {
		return errors.New("commit before transaction handle restore")
	}
	return s.retryCommitSink.Commit(ctx, id)
}

func TestRestorePreparedTransactionCommitsBeforeNewTransaction(t *testing.T) {
	sink := &restoredTransactionSink{retryCommitSink: retryCommitSink{failures: 1}}
	slot := &TaskSlot{TaskID: "sink", Operators: []Operator{sink}, RestoreCheckpoint: &TaskCheckpoint{
		TaskID: "sink", CheckpointID: 42, EpochID: 3, SinkPrepared: true,
		SinkCommittedCheckpoint: 41, Operators: [][]byte{[]byte("prepared-handle")},
	}}
	if err := slot.restoreCheckpoint(); err != nil {
		t.Fatal(err)
	}
	if err := slot.restoreSinkTransaction(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 2 || sink.BeginTxnCalls() != 0 || sink.AbortCallCount() != 0 {
		t.Fatalf("recovery changed prepared transaction: calls=%d", sink.calls)
	}
	if slot.RestoredCheckpointID != 42 {
		t.Fatal("restored checkpoint boundary not retained")
	}
}

func TestRestorePreparedTransactionRejectsNonTransactionalOperator(t *testing.T) {
	slot := &TaskSlot{Operators: []Operator{&noopMap{}}, RestoreCheckpoint: &TaskCheckpoint{SinkPrepared: true, CheckpointID: 42}}
	if err := slot.restoreSinkTransaction(t.Context()); err == nil {
		t.Fatal("prepared transaction was silently discarded")
	}
}

func TestFailedRecoveryCommitCannotReportRunning(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	sink := &restoredTransactionSink{retryCommitSink: retryCommitSink{failures: 100}}
	slot := NewTaskSlot(DefaultTaskSlotConfig(), nil, nil, []Operator{sink}, nil)
	slot.TaskID = "sink"
	slot.RestoreCheckpoint = &TaskCheckpoint{TaskID: "sink", CheckpointID: 42, EpochID: 3, SinkPrepared: true, Operators: [][]byte{[]byte("prepared-handle")}}
	running := false
	slot.OnRunning = func() { running = true }
	err := slot.Run(ctx)
	if !errors.Is(err, ErrCommitFailed) || running || sink.BeginTxnCalls() != 0 || sink.AbortCallCount() != 0 {
		t.Fatalf("failed recovery admitted new work or aborted: err=%v running=%v", err, running)
	}
}

func TestPreparedTransactionRejectsOtherEpochDecisions(t *testing.T) {
	for _, kind := range []ControlType{CtrlCommitCheckpoint, CtrlAbortTransaction, CtrlAbortCheckpoint, CtrlBarrierReceived} {
		for _, epoch := range []uint64{4, 6} {
			sink := &mockTransactionalSink{}
			cc := &chainContext{ctx: t.Context(), txnSink: sink, transactionPrepared: true, preparedCheckpoint: 7, preparedEpoch: 5}
			eof := 0
			if err := handleControl(cc, ControlMsg{Type: kind, CheckpointID: 7, EpochID: epoch}, &eof); err != nil {
				t.Fatal(err)
			}
			if !cc.transactionPrepared || sink.AbortCallCount() != 0 || len(sink.CommitCallIDs()) != 0 || sink.BeginTxnCalls() != 0 {
				t.Fatalf("kind=%v epoch=%d changed prepared transaction", kind, epoch)
			}
		}
	}
}
