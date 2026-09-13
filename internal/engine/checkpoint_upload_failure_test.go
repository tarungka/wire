package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReplicationFailureUsesCheckpointThreshold(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cc, channels := newTestCoordinator(CheckpointConfig{Timeout: time.Minute, MaxConsecutiveFailures: 2}, 1)
	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()
	for id := uint64(1); id <= 2; id++ {
		if err := cc.TriggerCheckpoint(ctx, id, 3); err != nil {
			t.Fatal(err)
		}
		if err := cc.FailCheckpoint(ctx, id, 3, errors.New("replica offline")); err != nil {
			t.Fatal(err)
		}
		select {
		case ctrl := <-channels[0]:
			if ctrl.Type != CtrlAbortCheckpoint || ctrl.CheckpointID != id {
				t.Fatalf("abort: %+v", ctrl)
			}
		case <-ctx.Done():
			t.Fatal("failure did not abort checkpoint")
		}
		if id == 1 {
			select {
			case err := <-done:
				t.Fatalf("first replication failure killed task: %v", err)
			default:
			}
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrMaxConsecutiveCheckpointFailures) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("threshold did not stop coordinator")
	}
}

func TestStaleReplicationFailureCannotAbortNewEpoch(t *testing.T) {
	ctx := context.Background()
	cc, channels := newTestCoordinator(CheckpointConfig{Timeout: time.Minute}, 1)
	if err := cc.TriggerCheckpoint(ctx, 8, 4); err != nil {
		t.Fatal(err)
	}
	defer func() { cc.mu.Lock(); cc.timer.Stop(); cc.mu.Unlock() }()
	for _, identity := range [][2]uint64{{7, 4}, {8, 3}} {
		if err := cc.abortCheckpointIdentity(ctx, identity[0], identity[1], errors.New("late upload")); err != nil {
			t.Fatal(err)
		}
	}
	cc.mu.Lock()
	active, epoch, failures := cc.activeCheckpointID, cc.activeEpochID, cc.consecutiveFailures
	cc.mu.Unlock()
	if active != 8 || epoch != 4 || failures != 0 {
		t.Fatalf("stale failure changed active state: %d %d %d", active, epoch, failures)
	}
	select {
	case ctrl := <-channels[0]:
		t.Fatalf("stale abort delivered: %+v", ctrl)
	default:
	}
}
