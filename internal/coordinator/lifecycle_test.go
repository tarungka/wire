package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestLifecycle_SingleNode(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, nil, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	// Wait for leader.
	time.Sleep(100 * time.Millisecond)
	if c.State() != StateLeader {
		t.Fatalf("expected LEADER, got %s", c.State())
	}

	// Graceful shutdown.
	cancel()
	err := <-done
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLifecycle_MultiNode_ElectionWin(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	election := NewNoopElection(":4001")
	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		ListenAddr:             ":4001",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, election, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	if c.State() != StateLeader {
		t.Fatalf("expected LEADER, got %s", c.State())
	}
	if !c.IsReady() {
		t.Fatal("expected ready")
	}

	cancel()
	err := <-done
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLifecycle_GracefulShutdown(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, nil, zerolog.Nop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Shutdown via coordinator method.
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	cancel()

	err := <-done
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLifecycle_GetLeaderInfo_SingleNode(t *testing.T) {
	store := NewMemoryStore()
	defer func() { _ = store.Close() }()

	c := New(CoordinatorConfig{
		NodeID:                 "n1",
		ListenAddr:             ":4001",
		HeartbeatFlushInterval: 50 * time.Millisecond,
	}, store, nil, zerolog.Nop())

	ctx := t.Context()

	go func() { _ = c.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	info, isSelf, err := c.GetLeaderInfo()
	if err != nil {
		t.Fatalf("GetLeaderInfo: %v", err)
	}
	if !isSelf {
		t.Fatal("expected isSelf=true")
	}
	if info.NodeID != "n1" {
		t.Fatalf("expected node-id n1, got %s", info.NodeID)
	}
	if info.Address != ":4001" {
		t.Fatalf("expected address :4001, got %s", info.Address)
	}
}
