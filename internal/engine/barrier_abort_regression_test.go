package engine

import (
	"context"
	"testing"
)

func TestAbortRejectsDelayedBarriersAndAllowsNextCheckpoint(t *testing.T) {
	aligner := NewBarrierAligner(2, 8)
	aligner.OnBarrier(0, 1, 5)
	if err := aligner.BufferEvent(context.Background(), 0, Event{Value: []byte("buffered")}); err != nil {
		t.Fatal(err)
	}
	if drained := aligner.AbortAlignment(1, 4); len(drained) != 0 || aligner.ActiveCheckpointID() != 1 {
		t.Fatal("stale epoch aborted alignment")
	}
	drained := aligner.AbortAlignment(1, 5)
	if len(drained) != 1 || aligner.BufferedBytes() != 0 {
		t.Fatal("abort did not release buffers")
	}
	for _, input := range []int{0, 1, 0, 1} {
		if aligner.OnBarrier(input, 1, 5) || aligner.ActiveCheckpointID() != 0 {
			t.Fatal("delayed barrier resurrected aborted checkpoint")
		}
	}
	aligner.OnBarrier(0, 2, 5)
	if !aligner.OnBarrier(1, 2, 5) {
		t.Fatal("next checkpoint failed to align")
	}
	aligner.FinishAlignment(2)
	if aligner.OnBarrier(0, 2, 5) || aligner.ActiveCheckpointID() != 0 {
		t.Fatal("completed barrier replayed")
	}
	aligner.AbortAlignment(3, 5)
	if aligner.OnBarrier(0, 3, 5) || aligner.ActiveCheckpointID() != 0 {
		t.Fatal("abort before first barrier was forgotten")
	}
}

func TestAbortDecisionIsTerminalWithoutReopeningTransaction(t *testing.T) {
	sink := &mockTransactionalSink{}
	cc := &chainContext{ctx: context.Background(), txnSink: sink, aligner: NewBarrierAligner(1, 4), cpMetrics: NoopCheckpointMetrics(), log: testLogger(), transactionPrepared: true, preparedCheckpoint: 7, preparedEpoch: 5}
	eof := 0
	for range 3 {
		if err := handleControl(cc, ControlMsg{Type: CtrlAbortTransaction, CheckpointID: 7, EpochID: 5}, &eof); err != ErrTransactionAborted {
			t.Fatalf("abort must require replay: %v", err)
		}
	}
	if sink.abortCalls != 1 || sink.beginTxnCalls != 0 || !cc.transactionAborted {
		t.Fatalf("abort reopened or repeated transaction: aborts=%d begins=%d", sink.abortCalls, sink.beginTxnCalls)
	}
	if err := handleControl(cc, ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 7, EpochID: 5}, &eof); err != ErrTransactionAborted {
		t.Fatalf("terminal abort accepted another decision: %v", err)
	}
	if len(sink.CommitCallIDs()) != 0 {
		t.Fatal("aborted transaction committed")
	}
}
