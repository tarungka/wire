package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBarrierAlignment_SingleInput(t *testing.T) {
	ba := NewBarrierAligner(1, 100)

	// Single input — barrier immediately aligns.
	aligned := ba.OnBarrier(0, 1, 1)
	if !aligned {
		t.Fatal("single input should align immediately")
	}
	if !ba.AllAligned(1) {
		t.Fatal("AllAligned should return true")
	}
}

func TestBarrierAlignment_TwoInputs(t *testing.T) {
	ba := NewBarrierAligner(2, 100)

	// First barrier from input 0.
	aligned := ba.OnBarrier(0, 1, 1)
	if aligned {
		t.Fatal("should not align after first barrier")
	}
	if ba.AllAligned(1) {
		t.Fatal("AllAligned should be false with only one barrier")
	}

	// Input 0 should now be aligning.
	if !ba.IsAligning(0) {
		t.Fatal("input 0 should be aligning")
	}
	// Input 1 should not be aligning.
	if ba.IsAligning(1) {
		t.Fatal("input 1 should not be aligning yet")
	}

	// Buffer some events for input 0.
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		err := ba.BufferEvent(ctx, 0, Event{Value: []byte{byte(i)}})
		if err != nil {
			t.Fatalf("BufferEvent: %v", err)
		}
	}

	// Second barrier from input 1.
	aligned = ba.OnBarrier(1, 1, 1)
	if !aligned {
		t.Fatal("should align after both barriers")
	}
	if !ba.AllAligned(1) {
		t.Fatal("AllAligned should be true")
	}

	// Drain should return the 3 buffered events from input 0.
	drained := ba.DrainAll(1)
	if len(drained) != 3 {
		t.Fatalf("DrainAll: got %d events, want 3", len(drained))
	}

	// Verify event order.
	for i, e := range drained {
		if e.Value[0] != byte(i) {
			t.Errorf("drained[%d]: got %d, want %d", i, e.Value[0], i)
		}
	}

	// Reset and verify clean state.
	ba.Reset(1)
	if ba.ActiveCheckpointID() != 0 {
		t.Fatal("ActiveCheckpointID should be 0 after reset")
	}
	if ba.IsAligning(0) {
		t.Fatal("input 0 should not be aligning after reset")
	}
}

func TestBarrierAlignment_StaggeredBarriers(t *testing.T) {
	ba := NewBarrierAligner(3, 100)
	ctx := context.Background()

	// Barrier from input 0.
	ba.OnBarrier(0, 1, 1)

	// Buffer events for input 0 while waiting for others.
	for i := 0; i < 5; i++ {
		ba.BufferEvent(ctx, 0, Event{Key: []byte("0"), Value: []byte{byte(i)}})
	}

	// Barrier from input 2 (skip 1).
	ba.OnBarrier(2, 1, 1)

	// Buffer events for input 2.
	for i := 0; i < 3; i++ {
		ba.BufferEvent(ctx, 2, Event{Key: []byte("2"), Value: []byte{byte(i)}})
	}

	// Barrier from input 1 completes alignment.
	aligned := ba.OnBarrier(1, 1, 1)
	if !aligned {
		t.Fatal("should align after all three barriers")
	}

	// Drain should return events in input order: 0, 1, 2.
	drained := ba.DrainAll(1)
	if len(drained) != 8 { // 5 from input 0, 0 from input 1, 3 from input 2
		t.Fatalf("DrainAll: got %d events, want 8", len(drained))
	}

	// First 5 should be from input 0.
	for i := 0; i < 5; i++ {
		if string(drained[i].Key) != "0" {
			t.Errorf("drained[%d]: expected input 0, got key %q", i, drained[i].Key)
		}
	}
	// Last 3 should be from input 2.
	for i := 5; i < 8; i++ {
		if string(drained[i].Key) != "2" {
			t.Errorf("drained[%d]: expected input 2, got key %q", i, drained[i].Key)
		}
	}
}

func TestBarrierAlignment_DrainOrdering(t *testing.T) {
	ba := NewBarrierAligner(2, 100)
	ctx := context.Background()

	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("a0")})
	ba.BufferEvent(ctx, 0, Event{Value: []byte("a1")})

	ba.OnBarrier(1, 1, 1)
	// No events buffered for input 1 — DrainAll should still work.

	drained := ba.DrainAll(1)
	if len(drained) != 2 {
		t.Fatalf("got %d events, want 2", len(drained))
	}
	if string(drained[0].Value) != "a0" || string(drained[1].Value) != "a1" {
		t.Errorf("wrong drain order: %v", drained)
	}
}

func TestBarrierAlignment_Reset(t *testing.T) {
	ba := NewBarrierAligner(2, 100)

	ba.OnBarrier(0, 1, 1)
	ba.Reset(1)

	// After reset, no input should be aligning.
	if ba.IsAligning(0) {
		t.Fatal("input 0 should not be aligning after reset")
	}
	if ba.ActiveCheckpointID() != 0 {
		t.Fatal("active checkpoint should be 0")
	}
}

func TestBarrierAlignment_AbortDrain(t *testing.T) {
	ba := NewBarrierAligner(2, 100)
	ctx := context.Background()

	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("buffered")})

	// Abort (drain + reset without completing alignment).
	drained := ba.DrainAll(1)
	if len(drained) != 1 {
		t.Fatalf("got %d events, want 1", len(drained))
	}

	ba.Reset(1)
	if ba.ActiveCheckpointID() != 0 {
		t.Fatal("should be reset")
	}
}

func TestBarrierAlignment_BufferFull(t *testing.T) {
	ba := NewBarrierAligner(1, 2) // Max 2 events.
	ctx := context.Background()

	ba.OnBarrier(0, 1, 1)

	// Buffer 2 events — should succeed.
	if err := ba.BufferEvent(ctx, 0, Event{Value: []byte("1")}); err != nil {
		t.Fatalf("BufferEvent 1: %v", err)
	}
	if err := ba.BufferEvent(ctx, 0, Event{Value: []byte("2")}); err != nil {
		t.Fatalf("BufferEvent 2: %v", err)
	}

	// Third event should fail.
	err := ba.BufferEvent(ctx, 0, Event{Value: []byte("3")})
	if err == nil {
		t.Fatal("expected ErrSideBufferFull")
	}
}

func TestBarrierAlignment_WrongCheckpointDrain(t *testing.T) {
	ba := NewBarrierAligner(1, 100)
	ctx := context.Background()

	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("x")})

	// DrainAll with wrong checkpoint ID should return nil.
	drained := ba.DrainAll(99)
	if drained != nil {
		t.Fatalf("expected nil drain for wrong checkpoint, got %d events", len(drained))
	}
}

func TestBarrierAlignment_CapacityReuse(t *testing.T) {
	ba := NewBarrierAligner(1, 100)
	ctx := context.Background()

	// First checkpoint cycle.
	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("a")})
	ba.DrainAll(1)
	ba.Reset(1)

	// Second checkpoint cycle — should reuse allocated buffer.
	ba.OnBarrier(0, 2, 2)
	err := ba.BufferEvent(ctx, 0, Event{Value: []byte("b")})
	if err != nil {
		t.Fatalf("BufferEvent after reset: %v", err)
	}
	drained := ba.DrainAll(2)
	if len(drained) != 1 || string(drained[0].Value) != "b" {
		t.Fatalf("unexpected drain result: %v", drained)
	}
}

func TestBarrierAlignment_DuplicateBarrierSameInput(t *testing.T) {
	// Sending the same barrier twice from the same input should not cause
	// alignment to complete for a 2-input aligner.
	ba := NewBarrierAligner(2, 100)

	ba.OnBarrier(0, 1, 1)
	aligned := ba.OnBarrier(0, 1, 1) // duplicate
	if aligned {
		t.Fatal("duplicate barrier from same input should not complete alignment")
	}
	if ba.AllAligned(1) {
		t.Fatal("AllAligned should be false with only one unique input")
	}
	// Input 1 still hasn't arrived.
	if ba.IsAligning(1) {
		t.Fatal("input 1 should not be aligning")
	}
}

func TestBarrierAlignment_ConcurrentAccess(t *testing.T) {
	// Designed to be run with -race to verify mutex protection.
	ba := NewBarrierAligner(4, 1000)
	ctx := context.Background()
	var wg sync.WaitGroup

	// Simulate 4 input readers concurrently calling OnBarrier and BufferEvent.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ba.OnBarrier(idx, 1, 1)
			for j := 0; j < 50; j++ {
				_ = ba.BufferEvent(ctx, idx, Event{Value: []byte{byte(j)}})
				_ = ba.IsAligning(idx)
				_ = ba.ActiveCheckpointID()
			}
		}(i)
	}

	// Simulate operator chain checking alignment and draining.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if ba.AllAligned(1) {
				_ = ba.DrainAll(1)
				ba.Reset(1)
				return
			}
		}
	}()

	wg.Wait()
	// Test passes if no race detected.
}

func TestBarrierAlignment_BufferEventErrorWrapsErrSideBufferFull(t *testing.T) {
	ba := NewBarrierAligner(1, 1)
	ctx := context.Background()

	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("fill")})

	err := ba.BufferEvent(ctx, 0, Event{Value: []byte("overflow")})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrSideBufferFull) {
		t.Fatalf("expected errors.Is(err, ErrSideBufferFull), got: %v", err)
	}
}

func TestBarrierAlignment_ActiveEpochSetOnFirstBarrierOnly(t *testing.T) {
	ba := NewBarrierAligner(2, 100)

	ba.OnBarrier(0, 1, 10)
	if ba.ActiveEpochID() != 10 {
		t.Fatalf("expected epoch 10, got %d", ba.ActiveEpochID())
	}

	// Second barrier arrives — epoch should remain as set by first.
	ba.OnBarrier(1, 1, 11)
	if ba.ActiveEpochID() != 10 {
		t.Fatalf("epoch should remain 10 (set by first barrier), got %d", ba.ActiveEpochID())
	}
}

func TestBarrierAligner_AlignmentStartTime(t *testing.T) {
	ba := NewBarrierAligner(2, 100)

	// No alignment active — should be zero.
	if !ba.AlignmentStartTime().IsZero() {
		t.Fatal("expected zero time before any barrier")
	}

	before := time.Now()
	ba.OnBarrier(0, 1, 1)
	after := time.Now()

	st := ba.AlignmentStartTime()
	if st.IsZero() {
		t.Fatal("expected non-zero time after first barrier")
	}
	if st.Before(before) || st.After(after) {
		t.Errorf("start time %v not between %v and %v", st, before, after)
	}

	// Second barrier should not change start time.
	ba.OnBarrier(1, 1, 1)
	if ba.AlignmentStartTime() != st {
		t.Error("start time changed after second barrier")
	}

	// Reset should clear it.
	ba.Reset(1)
	if !ba.AlignmentStartTime().IsZero() {
		t.Fatal("expected zero time after reset")
	}
}

func TestBarrierAligner_BufferedEventCount(t *testing.T) {
	ba := NewBarrierAligner(3, 100)
	ctx := context.Background()

	if ba.BufferedEventCount() != 0 {
		t.Fatal("expected 0 buffered events initially")
	}

	ba.OnBarrier(0, 1, 1)
	ba.BufferEvent(ctx, 0, Event{Value: []byte("a")})
	ba.BufferEvent(ctx, 0, Event{Value: []byte("b")})

	if ba.BufferedEventCount() != 2 {
		t.Fatalf("expected 2 buffered events, got %d", ba.BufferedEventCount())
	}

	ba.OnBarrier(1, 1, 1)
	ba.BufferEvent(ctx, 1, Event{Value: []byte("c")})

	if ba.BufferedEventCount() != 3 {
		t.Fatalf("expected 3 buffered events across inputs, got %d", ba.BufferedEventCount())
	}

	// Drain clears buffers.
	ba.OnBarrier(2, 1, 1)
	ba.DrainAll(1)
	if ba.BufferedEventCount() != 0 {
		t.Fatalf("expected 0 buffered events after drain, got %d", ba.BufferedEventCount())
	}
}
