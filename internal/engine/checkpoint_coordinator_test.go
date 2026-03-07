package engine

import (
	"context"
	"errors"
	"sync"
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

// waitForNoActiveCheckpoint polls until the coordinator has no active checkpoint.
func waitForNoActiveCheckpoint(t *testing.T, cc *CheckpointCoordinator, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cc.mu.Lock()
		active := cc.activeCheckpointID
		cc.mu.Unlock()
		if active == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for checkpoint to complete")
}

// waitForActiveCheckpoint polls until the coordinator's activeCheckpointID becomes 0
// (i.e., the checkpoint was aborted/completed), using a generous timeout.
func waitForCheckpointAborted(t *testing.T, cc *CheckpointCoordinator, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cc.mu.Lock()
		active := cc.activeCheckpointID
		cc.mu.Unlock()
		if active == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for checkpoint abort")
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

	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Verify state: no active checkpoint, consecutive failures reset.
	cc.mu.Lock()
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
	waitForCheckpointAborted(t, cc, 2*time.Second)

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
		waitForCheckpointAborted(t, cc, 2*time.Second)
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
		waitForCheckpointAborted(t, cc, 2*time.Second)
	}

	// 1 success — resets consecutive counter.
	if err := cc.TriggerCheckpoint(ctx, 3, 3); err != nil {
		t.Fatalf("TriggerCheckpoint(3): %v", err)
	}
	cc.AckCheckpoint(0, 3)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// 2 more failures — should still be under threshold (2 < 3).
	for i := uint64(4); i <= 5; i++ {
		if err := cc.TriggerCheckpoint(ctx, i, i); err != nil {
			t.Fatalf("TriggerCheckpoint(%d): %v", i, err)
		}
		waitForCheckpointAborted(t, cc, 2*time.Second)
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
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Second checkpoint — should be delayed by MinPause.
	start := time.Now()
	if err := cc.TriggerCheckpoint(ctx, 2, 2); err != nil {
		t.Fatalf("TriggerCheckpoint(2): %v", err)
	}
	elapsed := time.Since(start)

	// Should have waited at least ~100ms (MinPause minus processing time).
	if elapsed < 100*time.Millisecond {
		t.Errorf("MinPause not enforced: elapsed %v, expected >= 100ms", elapsed)
	}

	cc.AckCheckpoint(0, 2)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

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

	// Give coordinator time to process the stale ACK.
	// We poll until the ACK channel is drained (coordinator processed it).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(cc.ackCh) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

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
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

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
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Wait past the timeout to ensure no abort is sent.
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
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Checkpoint 2: success.
	if err := cc.TriggerCheckpoint(ctx, 2, 2); err != nil {
		t.Fatalf("TriggerCheckpoint(2): %v", err)
	}
	cc.AckCheckpoint(0, 2)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Checkpoint 3: failure (timeout).
	if err := cc.TriggerCheckpoint(ctx, 3, 3); err != nil {
		t.Fatalf("TriggerCheckpoint(3): %v", err)
	}
	waitForCheckpointAborted(t, cc, 2*time.Second) // Rate: 1/3 = 0.33, OK.

	// Checkpoint 4: failure (timeout).
	if err := cc.TriggerCheckpoint(ctx, 4, 4); err != nil {
		t.Fatalf("TriggerCheckpoint(4): %v", err)
	}
	waitForCheckpointAborted(t, cc, 2*time.Second) // Rate: 2/4 = 0.50, OK (not exceeded).

	// Checkpoint 5: failure — rate becomes 3/5 = 0.60 > 0.50.
	if err := cc.TriggerCheckpoint(ctx, 5, 5); err != nil {
		t.Fatalf("TriggerCheckpoint(5): %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, ErrCheckpointFailureRateExceeded) {
			t.Fatalf("expected ErrCheckpointFailureRateExceeded, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinator did not return error for exceeded failure rate")
	}
}

func TestCheckpointCoordinator_DuplicateACK(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}

	// Send ACK twice for same task — second should be ignored (stale).
	cc.AckCheckpoint(0, 1)
	cc.AckCheckpoint(0, 1)

	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	cancel()
	<-done
}

func TestCheckpointCoordinator_TriggerWhileActive(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger first checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint(1): %v", err)
	}

	// Trigger second checkpoint while first is active — should get ErrCheckpointAlreadyActive.
	err := cc.TriggerCheckpoint(ctx, 2, 2)
	if !errors.Is(err, ErrCheckpointAlreadyActive) {
		t.Fatalf("expected ErrCheckpointAlreadyActive, got: %v", err)
	}

	// Complete first checkpoint normally.
	cc.AckCheckpoint(0, 1)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	cancel()
	<-done
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

func TestCheckpointCoordinator_ConcurrentTrigger(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger first checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint(1): %v", err)
	}

	// Concurrently try to trigger more checkpoints — all should fail.
	const goroutines = 10
	var wg sync.WaitGroup
	errCount := make(chan int, 1)
	var mu sync.Mutex
	count := 0

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			err := cc.TriggerCheckpoint(ctx, id, id)
			if errors.Is(err, ErrCheckpointAlreadyActive) {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}(uint64(100 + i))
	}

	wg.Wait()
	errCount <- count

	got := <-errCount
	if got != goroutines {
		t.Errorf("expected %d ErrCheckpointAlreadyActive errors, got %d", goroutines, got)
	}

	// Complete original checkpoint.
	cc.AckCheckpoint(0, 1)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	cancel()
	<-done
}

// -- Transactional sink coordinator tests (WIP-10) --

func TestCheckpointCoordinator_CommitNotification(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, channels := newTestCoordinator(cfg, 2)

	// Register both tasks as transactional sinks.
	cc.RegisterTransactionalSink(0, "sink-0")
	cc.RegisterTransactionalSink(1, "sink-1")

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
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Both channels should receive CtrlCommitCheckpoint.
	for i, ch := range channels {
		select {
		case msg := <-ch:
			if msg.Type != CtrlCommitCheckpoint {
				t.Errorf("channel[%d]: expected CtrlCommitCheckpoint, got %v", i, msg.Type)
			}
			if msg.CheckpointID != 1 {
				t.Errorf("channel[%d]: checkpoint ID: got %d, want 1", i, msg.CheckpointID)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("channel[%d]: no commit notification received", i)
		}
	}

	cancel()
	<-done
}

func TestCheckpointCoordinator_CommitNotification_OnlyToRegisteredSinks(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, channels := newTestCoordinator(cfg, 3)

	// Only register task 1 as transactional sink.
	cc.RegisterTransactionalSink(1, "sink-1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger and complete checkpoint.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}
	cc.AckCheckpoint(0, 1)
	cc.AckCheckpoint(1, 1)
	cc.AckCheckpoint(2, 1)
	waitForNoActiveCheckpoint(t, cc, 2*time.Second)

	// Only channel[1] should receive CtrlCommitCheckpoint.
	select {
	case msg := <-channels[1]:
		if msg.Type != CtrlCommitCheckpoint {
			t.Errorf("channel[1]: expected CtrlCommitCheckpoint, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("channel[1]: no commit notification received")
	}

	// Channels 0 and 2 should not have received CtrlCommitCheckpoint.
	for _, idx := range []int{0, 2} {
		select {
		case msg := <-channels[idx]:
			if msg.Type == CtrlCommitCheckpoint {
				t.Errorf("channel[%d]: unexpected CtrlCommitCheckpoint", idx)
			}
		default:
			// Good — no message.
		}
	}

	cancel()
	<-done
}

func TestCheckpointCoordinator_AbortSendsTransactionAbort(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 100 * time.Millisecond}
	cc, channels := newTestCoordinator(cfg, 2)

	// Register both as transactional sinks.
	cc.RegisterTransactionalSink(0, "sink-0")
	cc.RegisterTransactionalSink(1, "sink-1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()

	// Trigger checkpoint but don't ACK — let it time out.
	if err := cc.TriggerCheckpoint(ctx, 1, 1); err != nil {
		t.Fatalf("TriggerCheckpoint: %v", err)
	}
	waitForCheckpointAborted(t, cc, 2*time.Second)

	// Each channel should receive both CtrlAbortCheckpoint and CtrlAbortTransaction.
	for i, ch := range channels {
		var gotAbortCheckpoint, gotAbortTransaction bool
		// Drain up to 2 messages per channel.
		for j := 0; j < 2; j++ {
			select {
			case msg := <-ch:
				switch msg.Type {
				case CtrlAbortCheckpoint:
					gotAbortCheckpoint = true
				case CtrlAbortTransaction:
					gotAbortTransaction = true
				}
			case <-time.After(2 * time.Second):
				t.Errorf("channel[%d]: timed out waiting for message %d", i, j)
			}
		}
		if !gotAbortCheckpoint {
			t.Errorf("channel[%d]: missing CtrlAbortCheckpoint", i)
		}
		if !gotAbortTransaction {
			t.Errorf("channel[%d]: missing CtrlAbortTransaction", i)
		}
	}

	cancel()
	<-done
}

func TestCheckpointCoordinator_PendingCommits_Recovery(t *testing.T) {
	cfg := CheckpointConfig{Timeout: 5 * time.Second}
	cc, _ := newTestCoordinator(cfg, 2)

	// Register both as transactional sinks.
	cc.RegisterTransactionalSink(0, "sink-0")
	cc.RegisterTransactionalSink(1, "sink-1")

	// Verify: with no committed checkpoints, both should be pending.
	pending := cc.PendingCommits(5)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending commits, got %d", len(pending))
	}

	// Simulate sink-0 committed up to checkpoint 5.
	cc.mu.Lock()
	cc.sinkTxnStates[0].LastCommittedCheckpoint = 5
	cc.mu.Unlock()

	pending = cc.PendingCommits(5)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending commit, got %d", len(pending))
	}
	if pending[0] != 1 {
		t.Errorf("expected pending task index 1, got %d", pending[0])
	}

	// Both committed.
	cc.mu.Lock()
	cc.sinkTxnStates[1].LastCommittedCheckpoint = 5
	cc.mu.Unlock()

	pending = cc.PendingCommits(5)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending commits, got %d", len(pending))
	}
}
