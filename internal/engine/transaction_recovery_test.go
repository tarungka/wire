package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The external ledger survives replacement of both the TaskSlot and connector.
// Tests invoke it serially; production connectors must implement the same fence
// atomically in their external system, including on each subsequent mutation.
type transactionLedger struct {
	Generation uint64
	Attempt    string
	Prepared   map[uint64][]string
	Visible    []string
	Committed  map[uint64]bool
}
type ledgerTransactionSink struct {
	mockTransactionalSink
	path         string
	authority    TransactionRecovery
	restored     uint64
	recoverCalls int
}

func (s *ledgerTransactionSink) read() (transactionLedger, error) {
	var ledger transactionLedger
	data, err := os.ReadFile(s.path)
	if err != nil {
		return ledger, err
	}
	err = json.Unmarshal(data, &ledger)
	return ledger, err
}
func (s *ledgerTransactionSink) save(ledger transactionLedger) error {
	data, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".next", data, 0600); err != nil {
		return err
	}
	return os.Rename(s.path+".next", s.path)
}
func (s *ledgerTransactionSink) RestoreCheckpoint(data []byte) error {
	return json.Unmarshal(data, &s.restored)
}
func (s *ledgerTransactionSink) RecoverTransactions(_ context.Context, authority TransactionRecovery) error {
	s.recoverCalls++
	ledger, err := s.read()
	if err != nil {
		return err
	}
	if authority.DeploymentGeneration < ledger.Generation || (authority.DeploymentGeneration == ledger.Generation && authority.AttemptID != ledger.Attempt) {
		return errors.New("obsolete external writer")
	}
	if authority.CompletedCheckpointID != s.restored {
		return errors.New("recovery before handle restoration")
	}
	ledger.Generation, ledger.Attempt = authority.DeploymentGeneration, authority.AttemptID
	for id := range ledger.Prepared {
		if id != authority.CompletedCheckpointID {
			delete(ledger.Prepared, id)
		}
	}
	if err := s.save(ledger); err != nil {
		return err
	}
	s.authority = authority
	return nil
}
func (s *ledgerTransactionSink) Commit(_ context.Context, id uint64) error {
	ledger, err := s.read()
	if err != nil {
		return err
	}
	if s.authority.DeploymentGeneration != ledger.Generation || s.authority.AttemptID != ledger.Attempt {
		return errors.New("obsolete commit")
	}
	if ledger.Committed[id] {
		return nil
	}
	records, ok := ledger.Prepared[id]
	if !ok || id != s.restored {
		return errors.New("missing prepared transaction")
	}
	ledger.Visible = append(ledger.Visible, records...)
	ledger.Committed[id] = true
	delete(ledger.Prepared, id)
	return s.save(ledger)
}

func TestRecoverOrphansPreservesOnlySelectedDecision(t *testing.T) {
	for _, boundary := range []uint64{0, 7} {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "external.json")
			old := &ledgerTransactionSink{path: path}
			if err := old.save(transactionLedger{Generation: 1, Attempt: "old", Prepared: map[uint64][]string{5: {"aborted-gap"}, 7: {"selected"}, 8: {"unacknowledged"}}, Committed: map[uint64]bool{4: true}, Visible: []string{"already-committed"}}); err != nil {
				t.Fatal(err)
			}
			// Reopen from disk twice: once for recovery, once to simulate a lost Commit response.
			for generation := uint64(2); generation <= 3; generation++ {
				sink := &ledgerTransactionSink{path: path}
				slot := &TaskSlot{CheckpointReplicator: ledgerRecoveryReplicator{}, TaskID: "sink", Operators: []Operator{sink}, TransactionRecovery: &TransactionRecovery{JobID: "job", TaskID: "sink", EpochID: 2, DeploymentGeneration: generation, AttemptID: "new"}}
				if boundary != 0 {
					slot.RestoreCheckpoint = &TaskCheckpoint{TaskID: "sink", CheckpointID: boundary, SinkPrepared: true, Operators: [][]byte{[]byte("7")}}
					if err := slot.restoreCheckpoint(); err != nil {
						t.Fatal(err)
					}
				}
				if err := slot.recoverSinkTransactions(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := slot.restoreSinkTransaction(t.Context()); err != nil {
					t.Fatal(err)
				}
				ledger, err := sink.read()
				if err != nil {
					t.Fatal(err)
				}
				expected := 1
				if boundary != 0 {
					expected++
				}
				if len(ledger.Prepared) != 0 || len(ledger.Visible) != expected || ledger.Visible[0] != "already-committed" {
					t.Fatalf("lost/duplicate/orphan output: %+v", ledger)
				}
				if boundary != 0 && ledger.Visible[1] != "selected" {
					t.Fatal(ledger.Visible)
				}
			}
			// An old recovery arriving after replacement cannot abort or overwrite the selected state.
			if err := old.RecoverTransactions(t.Context(), TransactionRecovery{DeploymentGeneration: 1, AttemptID: "old"}); err == nil {
				t.Fatal("stale recovery accepted")
			}
			// Even equal generations cannot be shared by different attempts.
			if err := old.RecoverTransactions(t.Context(), TransactionRecovery{DeploymentGeneration: 3, AttemptID: "other"}); err == nil {
				t.Fatal("conflicting writer accepted")
			}
		})
	}
}

func TestDistributedTransactionRecoveryFailsBeforeRunning(t *testing.T) {
	for _, kind := range []string{"missing-hook", "missing-authority", "rescaled-state"} {
		t.Run(kind, func(t *testing.T) {
			sink := &ledgerTransactionSink{}
			slot := NewTaskSlot(DefaultTaskSlotConfig(), nil, nil, []Operator{sink}, nil)
			slot.TaskID = "sink"
			slot.CheckpointReplicator = ledgerRecoveryReplicator{}
			slot.CheckpointReport = func(context.Context, uint64, uint64, error) error { return nil }
			slot.TransactionRecovery = &TransactionRecovery{JobID: "job", TaskID: "sink", EpochID: 1, DeploymentGeneration: 1, AttemptID: "attempt"}
			switch kind {
			case "missing-hook":
				slot.Operators = []Operator{&mockTransactionalSink{}}
			case "missing-authority":
				slot.TransactionRecovery.DeploymentGeneration = 0
			case "rescaled-state":
				slot.RestoredCheckpointID = 7
			}
			running := false
			slot.OnRunning = func() { running = true }
			if err := slot.Run(t.Context()); err == nil {
				t.Fatal("unsafe transactional startup accepted")
			}
			if running || sink.recoverCalls != 0 || sink.BeginTxnCalls() != 0 {
				t.Fatal("unsafe startup touched external state or admitted work")
			}
		})
	}
}

type ledgerRecoveryReplicator struct{}

func (ledgerRecoveryReplicator) Replicate(context.Context, TaskCheckpoint) error {
	return nil
}
