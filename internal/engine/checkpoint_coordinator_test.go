package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// helper: create coordinator with N task slots and given config.
func newTestCoordinator(cfg CheckpointConfig, numTasks int) (*CheckpointCoordinator, []chan ControlMsg) {
	channels := make([]chan ControlMsg, numTasks)
	sendChannels := make([]chan<- ControlMsg, numTasks)
	for i := range channels {
		channels[i] = make(chan ControlMsg, 10)
		sendChannels[i] = channels[i]
	}
	cc := NewCheckpointCoordinator(cfg, sendChannels, NoopCheckpointMetrics(), testLogger())
	return cc, channels
}

func TestCheckpointCoordinator_NormalCompletion(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}

	// ACK from both tasks.
	cc.AckCheckpoint(0, 1)
	cc.AckCheckpoint(1, 1)

	// Give coordinator time to process.
	time.Sleep(50 * time.Millisecond)

	// Verify state: no active checkpoint, consecutive failures reset.
	cc.mu.Lock()
	if cc.activeCheckpointID != 0 {
		t.Errorf("expected no active checkpoint, got %d", cc.activeCheckpointID)
	}
	if cc.consecutiveFailures != 0 {
		t.Errorf("expected 0 consecutive failures, got %d", cc.consecutiveFailures)
	}
	cc.mu.Unlock()

	cancel()
	<-done
}

func TestCheckpointCoordinator_TimeoutAbort(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 100 * time.Millisecond}
	cc, channels := newTestCoordinator(cfg, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger checkpoint but don't ACK.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}

	// Wait for timeout and abort.
	time.Sleep(300 * time.Millisecond)

	// Verify abort messages sent to both channels.
	for i, ch := range channels {
		select {
		case msg := <-ch:
			if msg.Type != CtrlAbortCheckpoint {
				t.Errorf("channel[%d]: expected CtrlAbortCheckpoint, got %v", i, msg.Type)
			}
			if msg.CheckpointID != 1 {
				t.Errorf("channel[%d]: checkpoint ID: got %d, want 1", i, msg.CheckpointID)
			}
		default:
			t.Errorf("channel[%d]: no abort message received", i)
		}
	}

	// Verify failures incremented.
	cc.mu.Lock()
	if cc.consecutiveFailures != 1 {
		t.Errorf("expected 1 consecutive failure, got %d", cc.consecutiveFailures)
	}
	cc.mu.Unlock()

	cancel()
	<-done
}

func TestCheckpointCoordinator_ConsecutiveFailuresExceedThreshold(t *testing.T) {
	cfg := CheckpointConfig{
		Timeout:                100 * time.Millisecond,
		MaxConsecutiveFailures: 3,
	}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger 3 checkpoints, all time out.
	for i := uint64(1); i <= 3; i++ {
		if err := cc.TriggerCheckpoint(ctx, i, i); err != nil {
			t.Fatalf("TriggerCheckpoint(%d): %v", i, err)
		}
		// Wait for timeout.
		time.Sleep(200 * time.Millisecond)
	}

	// The third timeout should cause a fatal error.
	select {
	case err := <-done:
		if !errors.Is(err, ErrMaxConsecutiveCheckpointFailures) {
			t.Fatalf("expected ErrMaxConsecutiveCheckpointFailures, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not return error")
	}
}

func TestCheckpointCoordinator_ConsecutiveFailuresResetOnSuccess(t *testing.T) {
	cfg := CheckpointConfig{
		Timeout:                100 * time.Millisecond,
		MaxConsecutiveFailures: 3,
	}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// 2 failures.
	for i := uint64(1); i <= 2; i++ {
		if err := cc.TriggerCheckpoint(ctx, i, i); err != nil {
			t.Fatalf("TriggerCheckpoint(%d): %v", i, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// 1 success — resets consecutive counter.
	if err := cc.TriggerCheckpoint(ctx, 3, 3); err != nil {
		t.Fatalf("TriggerCheckpoint(3): %v", err)
	}
	cc.AckCheckpoint(0, 3)
	time.Sleep(50 * time.Millisecond)

	// 2 more failures — should still be under threshold (2 < 3).
	for i := uint64(4); i <= 5; i++ {
		if err := cc.TriggerCheckpoint(ctx, i, i); err != nil {
			t.Fatalf("TriggerCheckpoint(%d): %v", i, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	cc.mu.Lock()
	consec := cc.consecutiveFailures
	cc.mu.Unlock()

	if consec != 2 {
		t.Errorf("expected 2 consecutive failures after reset, got %d", consec)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not shut down")
	}
}

func TestCheckpointCoordinator_MinPauseEnforcement(t *testing.T) {
	cfg := CheckpointConfig{
		Timeout:  5 * time.Second,
		MinPause: 200 * time.Millisecond,
	}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// First checkpoint — complete immediately.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint(1): %v", err)
	}
	cc.AckCheckpoint(0, 1)
	time.Sleep(50 * time.Millisecond)

	// Second checkpoint — should be delayed by MinPause.
	start := time.Now()
	if err := cc.TriggerCheckpoint(ctx, 2, 2); err != nil {
		t.Fatalf("TriggerCheckpoint(2): %v", err)
	}
	elapsed := time.Since(start)

	// Should have waited at least ~150ms (MinPause minus the 50ms already elapsed).
	if elapsed < 100*time.Millisecond {
		t.Errorf("MinPause not enforced: elapsed %v, expected >= 100ms", elapsed)
	}

	cc.AckCheckpoint(0, 2)
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-done
}

func TestCheckpointCoordinator_StaleACKIgnored(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger checkpoint 2.
	if err := cc.TriggerCheckpoint(ctx, 2, 2); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}

	// Send stale ACK for checkpoint 1 — should be ignored.
	cc.AckCheckpoint(0, 1)
	time.Sleep(50 * time.Millisecond)

	cc.mu.Lock()
	if cc.activeCheckpointID != 2 {
		t.Errorf("expected checkpoint 2 still active, got %d", cc.activeCheckpointID)
	}
	pending := len(cc.pendingACKs)
	cc.mu.Unlock()

	if pending != 1 {
		t.Errorf("expected 1 pending ACK, got %d", pending)
	}

	// Complete properly.
	cc.AckCheckpoint(0, 2)
	time.Sleep(50 * time.Millisecond)

	cancel()
	<-done
}

func TestCheckpointCoordinator_AbortAfterCompletion(t *testing.T) {
	// Timer fires after all ACKs — should be a no-op.
	cfg := CheckpointConfig{Timeout: 100 * time.Millisecond}
	cc, channels := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger and immediately complete.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}
	cc.AckCheckpoint(0, 1)
	time.Sleep(50 * time.Millisecond) // Complete before timer.

	// Wait past the timeout.
	time.Sleep(200 * time.Millisecond)

	// No abort should have been sent.
	select {
	case msg := <-channels[0]:
		t.Errorf("unexpected control message after completion: %+v", msg)
	default:
		// Good — no message.
	}

	cancel()
	<-done
}

func TestCheckpointCoordinator_TolerableFailureRate(t *testing.T) {
	cfg := CheckpointConfig{
		Timeout:              100 * time.Millisecond,
		TolerableFailureRate: 0.5,
	}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Checkpoint 1: success.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint(1): %v", err)
	}
	cc.AckCheckpoint(0, 1)
	time.Sleep(50 * time.Millisecond)

	// Checkpoint 2: success.
	if err := cc.TriggerCheckpoint(ctx, 2, 2); err != nil {
		t.Fatalf("TriggerCheckpoint(2): %v", err)
	}
	cc.AckCheckpoint(0, 2)
	time.Sleep(50 * time.Millisecond)

	// Checkpoint 3: failure (timeout).
	if err := cc.TriggerCheckpoint(ctx, 3, 3); err != nil {
		t.Fatalf("TriggerCheckpoint(3): %v", err)
	}
	time.Sleep(200 * time.Millisecond) // Rate: 1/3 = 0.33, OK.

	// Checkpoint 4: failure (timeout).
	if err := cc.TriggerCheckpoint(ctx, 4, 4); err != nil {
		t.Fatalf("TriggerCheckpoint(4): %v", err)
	}
	time.Sleep(200 * time.Millisecond) // Rate: 2/4 = 0.50, OK (not exceeded).

	// Checkpoint 5: failure — rate becomes 3/5 = 0.60 > 0.50.
	if err := cc.TriggerCheckpoint(ctx, 5, 5); err != nil {
		t.Fatalf("TriggerCheckpoint(5): %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrMaxConsecutiveCheckpointFailures) {
			t.Fatalf("expected ErrMaxConsecutiveCheckpointFailures (rate exceeded), got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinator did not return error for exceeded failure rate")
	}
}

func TestCheckpointCoordinator_ContextCancel(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger a checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}

	// Cancel context.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on context cancel, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not shut down on context cancel")
	}
}
