package coordinator

import (
	"context"
	"testing"
)

func TestNoopElection_Campaign(t *testing.T) {
	e := NewNoopElection(":4001")

	lctx, err := e.Campaign(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	if lctx.Epoch != 1 {
		t.Fatalf("expected epoch 1, got %d", lctx.Epoch)
	}
	if lctx.Ctx.Err() != nil {
		t.Fatal("leader context should not be canceled")
	}
}

func TestNoopElection_GetLeader(t *testing.T) {
	e := NewNoopElection(":4001")

	// Before campaign, no leader.
	_, _, err := e.GetLeader(context.Background())
	if err != ErrNoLeader {
		t.Fatalf("expected ErrNoLeader, got %v", err)
	}

	// After campaign, returns self.
	if _, err = e.Campaign(context.Background(), "node-1"); err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	nodeID, addr, err := e.GetLeader(context.Background())
	if err != nil {
		t.Fatalf("GetLeader: %v", err)
	}
	if nodeID != "node-1" {
		t.Fatalf("expected node-1, got %s", nodeID)
	}
	if addr != ":4001" {
		t.Fatalf("expected :4001, got %s", addr)
	}
}

func TestNoopElection_Resign(t *testing.T) {
	e := NewNoopElection(":4001")
	lctx, _ := e.Campaign(context.Background(), "node-1")

	if err := e.Resign(context.Background()); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if lctx.Ctx.Err() == nil {
		t.Fatal("leader context should be canceled after resign")
	}
}

func TestNoopElection_Close(t *testing.T) {
	e := NewNoopElection(":4001")
	lctx, _ := e.Campaign(context.Background(), "node-1")

	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if lctx.Ctx.Err() == nil {
		t.Fatal("leader context should be canceled after close")
	}
}

func TestNoopElection_ContextPropagation(t *testing.T) {
	e := NewNoopElection(":4001")

	// Create a cancellable parent context.
	parentCtx, cancel := context.WithCancel(context.Background())

	lctx, err := e.Campaign(parentCtx, "node-1")
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}

	// Leader context should be alive.
	if lctx.Ctx.Err() != nil {
		t.Fatal("leader context should not be canceled initially")
	}

	// Cancel the parent — leader context must propagate cancellation.
	cancel()

	select {
	case <-lctx.Ctx.Done():
		// Expected: parent cancellation propagated.
	default:
		t.Fatal("canceling parent context should cancel leader context")
	}
}
