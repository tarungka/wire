package coordinator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileLockElection_Campaign(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")
	e := NewFileLockElection(lockPath, ":4001")
	defer func() { _ = e.Close() }()

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
	defer func() { _ = e.Close() }()

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
	_ = e1.Close()

	// Second election.
	e2 := NewFileLockElection(lockPath, ":4002")
	lctx2, _ := e2.Campaign(context.Background(), "node-2")
	epoch2 := lctx2.Epoch
	_ = e2.Close()

	if epoch2 <= epoch1 {
		t.Fatalf("epoch should be monotonically increasing: %d <= %d", epoch2, epoch1)
	}
}

func TestFileLockElection_SecondProcessBlocked(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "leader.lock")

	// First process acquires lock.
	e1 := NewFileLockElection(lockPath, ":4001")
	defer func() { _ = e1.Close() }()
	_, err := e1.Campaign(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("Campaign e1: %v", err)
	}

	// Second process should time out trying to acquire.
	e2 := NewFileLockElection(lockPath, ":4002")
	defer func() { _ = e2.Close() }()

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

func TestFileLockElectionRejectsInvalidEpochWithoutOverwriting(t *testing.T) {
	for _, data := range [][]byte{{}, {1, 2}, {255, 255, 255, 255, 255, 255, 255, 255}} {
		t.Run(fmt.Sprintf("%x", data), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "leader.lock")
			if err := os.WriteFile(path+".epoch", data, 0o600); err != nil {
				t.Fatal(err)
			}
			election := NewFileLockElection(path, ":4001")
			defer election.Close()
			if _, err := election.Campaign(context.Background(), "node"); err == nil {
				t.Fatal("accepted corrupt or exhausted epoch")
			}
			got, err := os.ReadFile(path + ".epoch")
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("overwrote fencing evidence: %x %v", got, err)
			}
			// Failed campaigning must release the lock, including on epoch errors.
			if err := os.Remove(path + ".epoch"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := election.Campaign(ctx, "node"); err != nil {
				t.Fatalf("failed campaign leaked lock: %v", err)
			}
		})
	}
}

func TestFileLockElectionParentCancellationRevokesAuthority(t *testing.T) {
	election := NewFileLockElection(filepath.Join(t.TempDir(), "leader.lock"), ":4001")
	defer election.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	grant, err := election.Campaign(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if grant.Ctx.Err() == nil {
		t.Fatal("election grant outlived its owner")
	}
}
