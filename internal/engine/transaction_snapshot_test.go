package engine

import (
	"context"
	"errors"
	"testing"
)

type preparedSnapshotSink struct {
	mockTransactionalSink
	prepared    bool
	captured    bool
	snapshotErr error
}

func (s *preparedSnapshotSink) PreCommit(context.Context, uint64) error {
	if s.preCommitErr != nil {
		return s.preCommitErr
	}
	s.prepared = true
	return nil
}

func (s *preparedSnapshotSink) Checkpoint(uint64) ([]byte, error) {
	if !s.prepared {
		return nil, errors.New("snapshot cannot recover a transaction that has not been prepared")
	}
	s.captured = true
	return []byte("durable-prepared-transaction-handle"), s.snapshotErr
}

func TestTransactionalSnapshotFollowsPrepareBeforeACK(t *testing.T) {
	for _, failure := range []string{"none", "prepare", "snapshot"} {
		t.Run(failure, func(t *testing.T) {
			sink := &preparedSnapshotSink{}
			if failure == "prepare" {
				sink.preCommitErr = errors.New("prepare failed")
			}
			if failure == "snapshot" {
				sink.snapshotErr = errors.New("snapshot failed")
			}
			aligner := NewBarrierAligner(1, 100)
			aligner.OnBarrier(0, 7, 3)
			acked := false
			cc := &chainContext{
				ctx: t.Context(), txnSink: sink,
				links:   buildChainLinks([]Operator{sink}, nil),
				inputCh: make(chan Event), aligner: aligner,
				cpMetrics: NoopCheckpointMetrics(), log: testLogger(),
				ackFn: func(id uint64) {
					if id != 7 || !sink.captured {
						t.Fatal("ACK preceded recoverable sink snapshot")
					}
					acked = true
				},
			}
			eof := 0
			err := handleControl(cc, ControlMsg{Type: CtrlBarrierReceived, CheckpointID: 7, EpochID: 3}, &eof)
			if failure == "none" {
				if err != nil || !acked || !cc.transactionPrepared {
					t.Fatalf("prepare/snapshot/ACK sequence failed: err=%v ack=%v prepared=%v", err, acked, cc.transactionPrepared)
				}
			} else if err == nil || acked {
				t.Fatalf("failed checkpoint was acknowledged: err=%v ack=%v", err, acked)
			}
			if failure == "prepare" && sink.captured {
				t.Fatal("failed preparation must not be snapshotted")
			}
		})
	}
}
