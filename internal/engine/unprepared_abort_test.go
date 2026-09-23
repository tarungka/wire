package engine

import (
	"context"
	"testing"
)

func TestCheckpointAbortPreservesUnpreparedTransaction(t *testing.T) {
	for _, kind := range []ControlType{CtrlAbortCheckpoint, CtrlAbortTransaction} {
		t.Run(kindName(kind), func(t *testing.T) {
			sink := &mockTransactionalSink{}
			cc := &chainContext{ctx: context.Background(), txnSink: sink, links: []ChainLink{{Operator: sink}}, aligner: NewBarrierAligner(2, 8), numInputs: 2, cpMetrics: NoopCheckpointMetrics(), errMetrics: NoopErrorMetrics(), log: testLogger()}
			if err := processEvent(cc, Event{Value: []byte("before")}); err != nil {
				t.Fatal(err)
			}
			cc.aligner.OnBarrier(0, 1, 5)
			if err := cc.aligner.BufferEvent(cc.ctx, 0, Event{Value: []byte("buffered")}); err != nil {
				t.Fatal(err)
			}
			eof := 0
			if err := handleControl(cc, ControlMsg{Type: kind, CheckpointID: 1, EpochID: 5}, &eof); err != nil {
				t.Fatalf("unprepared abort failed task: %v", err)
			}
			if sink.AbortCallCount() != 0 || cc.transactionAborted || !cc.transactionDirty {
				t.Fatal("unprepared transaction was rolled back")
			}
			if cc.aligner.OnBarrier(1, 1, 5) || cc.aligner.ActiveCheckpointID() != 0 {
				t.Fatal("late aborted barrier revived alignment")
			}
			if err := processEvent(cc, Event{Value: []byte("after")}); err != nil {
				t.Fatal(err)
			}
			cc.aligner.OnBarrier(0, 2, 5)
			cc.aligner.OnBarrier(1, 2, 5)
			if err := handleControl(cc, ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 2, EpochID: 5}, &eof); err != nil {
				t.Fatal(err)
			}
			if err := handleControl(cc, ControlMsg{Type: CtrlCommitCheckpoint, CheckpointID: 2, EpochID: 5}, &eof); err != nil {
				t.Fatal(err)
			}
			if len(sink.written) != 3 || len(sink.CommitCallIDs()) != 1 || sink.CommitCallIDs()[0] != 2 {
				t.Fatalf("next checkpoint lost records: records=%d commits=%v", len(sink.written), sink.CommitCallIDs())
			}
		})
	}
}
func kindName(kind ControlType) string {
	if kind == CtrlAbortCheckpoint {
		return "checkpoint"
	}
	return "transaction"
}
