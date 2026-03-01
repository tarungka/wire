package coordinator

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"
)

func TestValidateEpoch_Accept(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.epoch = 5
	c.state = StateLeader
	c.mu.Unlock()

	// Worker epoch <= coordinator epoch should be accepted.
	if err := c.ValidateEpoch(5); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := c.ValidateEpoch(3); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := c.ValidateEpoch(0); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateEpoch_Reject(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.epoch = 5
	c.state = StateLeader
	c.mu.Unlock()

	// Worker epoch > coordinator epoch should be rejected.
	err := c.ValidateEpoch(6)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("expected ErrStaleEpoch, got %v", err)
	}
}

func TestCurrentEpoch(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.mu.Lock()
	c.epoch = 42
	c.mu.Unlock()

	if got := c.CurrentEpoch(); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

func TestEpochMonotonicity_AfterRecovery(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	// Simulate a previous epoch stored.
	c := New(CoordinatorConfig{NodeID: "n1"}, store, nil, zerolog.Nop())
	c.persistEpoch(10)

	// Recovery should increment.
	state, err := recoverFromStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if state.epoch <= 10 {
		t.Fatalf("expected epoch > 10, got %d", state.epoch)
	}
}
