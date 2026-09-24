package sdk

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"
)

// The ledger models an external transaction service that outlives worker task
// instances. It fences writers and applies each checkpoint's commit once.
type pauseTransactionLedger struct {
	mu           sync.Mutex
	generation   uint64
	attempt      string
	prepared     map[uint64][]string
	committed    map[uint64]bool
	visible      []string
	loseResponse bool
}
type pauseTransactionSink struct {
	ledger    *pauseTransactionLedger
	observed  *collectSink
	authority TransactionRecovery
	active    []string
	prepared  uint64
}

func (*pauseTransactionSink) Open(context.Context) error { return nil }
func (*pauseTransactionSink) Close() error               { return nil }
func (s *pauseTransactionSink) fenced(fn func() error) error {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	if s.ledger.generation != s.authority.DeploymentGeneration || s.ledger.attempt != s.authority.AttemptID {
		return fmt.Errorf("stale transaction writer")
	}
	return fn()
}
func (s *pauseTransactionSink) RecoverTransactions(_ context.Context, authority TransactionRecovery) error {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	if authority.DeploymentGeneration < s.ledger.generation || (authority.DeploymentGeneration == s.ledger.generation && authority.AttemptID != s.ledger.attempt) {
		return fmt.Errorf("stale transaction recovery")
	}
	s.ledger.generation = authority.DeploymentGeneration
	s.ledger.attempt = authority.AttemptID
	s.authority = authority
	for id := range s.ledger.prepared {
		if id != authority.CompletedCheckpointID {
			delete(s.ledger.prepared, id)
		}
	}
	return nil
}
func (s *pauseTransactionSink) BeginTransaction(context.Context) error {
	return s.fenced(func() error { s.active = nil; return nil })
}
func (s *pauseTransactionSink) Write(ctx context.Context, e Event) error {
	return s.fenced(func() error { s.active = append(s.active, string(e.Value)); return s.observed.Write(ctx, e) })
}
func (s *pauseTransactionSink) PreCommit(_ context.Context, id uint64) error {
	return s.fenced(func() error {
		s.ledger.prepared[id] = append([]string(nil), s.active...)
		s.active = nil
		s.prepared = id
		return nil
	})
}
func (s *pauseTransactionSink) Checkpoint(uint64) ([]byte, error) {
	return binary.BigEndian.AppendUint64(nil, s.prepared), nil
}
func (s *pauseTransactionSink) RestoreCheckpoint(state []byte) error {
	if len(state) != 8 {
		return fmt.Errorf("invalid prepared transaction")
	}
	s.prepared = binary.BigEndian.Uint64(state)
	return nil
}
func (s *pauseTransactionSink) Commit(_ context.Context, id uint64) error {
	return s.fenced(func() error {
		if s.ledger.committed[id] {
			return nil
		}
		records, ok := s.ledger.prepared[id]
		if !ok {
			return fmt.Errorf("missing prepared checkpoint %d", id)
		}
		s.ledger.visible = append(s.ledger.visible, records...)
		s.ledger.committed[id] = true
		delete(s.ledger.prepared, id)
		if s.ledger.loseResponse {
			s.ledger.loseResponse = false
			return fmt.Errorf("injected lost commit response")
		}
		return nil
	})
}
func (s *pauseTransactionSink) Abort(context.Context) error {
	return s.fenced(func() error { s.active = nil; return nil })
}
