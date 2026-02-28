package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWatermarkPropagator_BasicEmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tracker := NewInputWatermarkTracker(2)
	// Disable idle detection by using zero idle timeout via the propagator.
	outputCh := make(chan OutputMsg, 100)

	// Set up watermarks and activity.
	tracker.RecordActivity(0)
	tracker.RecordActivity(1)
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)

	// Run propagator briefly, then cancel.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	_ = runWatermarkPropagator(ctx, tracker, outputCh, 50*time.Millisecond, 0, testLogger())
	close(outputCh)

	// Should have emitted at least one watermark with value 100 (min of inputs).
	var foundWM bool
	for msg := range outputCh {
		if msg.Type == OutputWatermark {
			foundWM = true
			if msg.Watermark.Timestamp > 200 {
				t.Errorf("watermark too high: got %d", msg.Watermark.Timestamp)
			}
		}
	}
	if !foundWM {
		t.Error("expected at least one watermark emission")
	}
}

func TestWatermarkPropagator_AdvanceOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tracker := NewInputWatermarkTracker(1)
	tracker.RecordActivity(0)
	tracker.AdvanceWatermark(0, 100)
	outputCh := make(chan OutputMsg, 100)

	// Run for a bit, then cancel.
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_ = runWatermarkPropagator(ctx, tracker, outputCh, 50*time.Millisecond, 0, testLogger())
	close(outputCh)

	// All emitted watermarks should be monotonically non-decreasing.
	var prev int64
	for msg := range outputCh {
		if msg.Type != OutputWatermark {
			continue
		}
		if msg.Watermark.Timestamp < prev {
			t.Errorf("non-monotonic watermark: %d after %d", msg.Watermark.Timestamp, prev)
		}
		prev = msg.Watermark.Timestamp
	}
}

func TestWatermarkPropagator_AllIdleSkip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tracker := NewInputWatermarkTracker(2)
	// Don't record any activity — all inputs are idle with non-zero timeout.
	tracker.AdvanceWatermark(0, 100)
	tracker.AdvanceWatermark(1, 200)
	outputCh := make(chan OutputMsg, 100)

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	_ = runWatermarkPropagator(ctx, tracker, outputCh, 50*time.Millisecond, time.Minute, testLogger())
	close(outputCh)

	// Should NOT have emitted any watermarks (all idle).
	for msg := range outputCh {
		if msg.Type == OutputWatermark {
			t.Error("expected no watermark emission when all inputs are idle")
		}
	}
}

func TestWatermarkPropagator_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	tracker := NewInputWatermarkTracker(1)
	outputCh := make(chan OutputMsg, 10)

	done := make(chan error, 1)
	go func() {
		done <- runWatermarkPropagator(ctx, tracker, outputCh, 50*time.Millisecond, 0, testLogger())
	}()

	// Cancel immediately.
	cancel()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("propagator did not exit on cancel")
	}
}

func TestWatermarkPropagator_NoAdvanceSkipsEmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tracker := NewInputWatermarkTracker(1)
	tracker.RecordActivity(0)
	tracker.AdvanceWatermark(0, 100)
	outputCh := make(chan OutputMsg, 100)

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	_ = runWatermarkPropagator(ctx, tracker, outputCh, 50*time.Millisecond, 0, testLogger())
	close(outputCh)

	// Watermark doesn't change, so after the first emission at 100,
	// subsequent ticks should not emit duplicates.
	var count int
	for msg := range outputCh {
		if msg.Type == OutputWatermark {
			count++
		}
	}
	// Should have exactly 1 emission (the initial one at 100).
	if count != 1 {
		t.Errorf("expected 1 watermark emission (no advance), got %d", count)
	}
}
