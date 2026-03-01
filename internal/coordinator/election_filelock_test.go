package coordinator

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFileLockElection_Campaign(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")
	e := NewFileLockElection(lockPath, ":4001")
	defer e.Close()

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

func TestFileLockElection_GetLeader(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")
	e := NewFileLockElection(lockPath, ":4001")
	defer e.Close()

	// Before campaign, no leader.
	_, _, err := e.GetLeader(context.Background())
	if err != ErrNoLeader {
		t.Fatalf("expected ErrNoLeader, got %v", err)
	}

	// After campaign, returns self.
	e.Campaign(context.Background(), "node-1")
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

func TestFileLockElection_Resign(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")
	e := NewFileLockElection(lockPath, ":4001")

	lctx, _ := e.Campaign(context.Background(), "node-1")
	if err := e.Resign(context.Background()); err != nil {
		t.Fatalf("Resign: %v", err)
	}
	if lctx.Ctx.Err() == nil {
		t.Fatal("leader context should be canceled after resign")
	}
}

func TestFileLockElection_EpochMonotonicity(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")

	// First election.
	e1 := NewFileLockElection(lockPath, ":4001")
	lctx1, _ := e1.Campaign(context.Background(), "node-1")
	epoch1 := lctx1.Epoch
	e1.Close()

	// Second election.
	e2 := NewFileLockElection(lockPath, ":4002")
	lctx2, _ := e2.Campaign(context.Background(), "node-2")
	epoch2 := lctx2.Epoch
	e2.Close()

	if epoch2 <= epoch1 {
		t.Fatalf("epoch should be monotonically increasing: %d <= %d", epoch2, epoch1)
	}
}

func TestFileLockElection_SecondProcessBlocked(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")

	// First process acquires lock.
	e1 := NewFileLockElection(lockPath, ":4001")
	defer e1.Close()
	_, err := e1.Campaign(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Campaign e1: %v", err)
	}

	// Second process should time out trying to acquire.
	e2 := NewFileLockElection(lockPath, ":4002")
	defer e2.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = e2.Campaign(ctx, "node-2")
	if err == nil {
		t.Fatal("second campaign should have failed (lock held)")
	}
	if ctx.Err() == nil {
		t.Fatal("expected context timeout")
	}
}
