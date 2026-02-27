package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

// newTestPipeline creates a source→operator→sink pipeline connected by
// FrameStreams for integration testing. Returns (inputWriter, outputReader)
// streams for external interaction, plus the TaskSlot.
func newTestPipeline(t *testing.T, operators []Operator, source SourceOperator) (inputWriter *transport.FrameStream, outputReader *transport.FrameStream, ts *TaskSlot) {
	t.Helper()
	cfg := DefaultTaskSlotConfig()
	cfg.WatermarkInterval = 50 * time.Millisecond // Faster for tests.

	var inputs []*transport.FrameStream
	if source == nil {
		iw, ir := newTestStreamPair(t)
		inputWriter = iw
		inputs = []*transport.FrameStream{ir}
	}

	ow, or := newTestStreamPair(t)
	outputReader = or
	outputs := []*transport.FrameStream{ow}

	ts = NewTaskSlot(cfg, inputs, outputs, operators, source)
	return
}

func TestTaskSlot_SourceMapSink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sink := &atomicCountingSink{}
	batches := [][]Event{
		{{Value: []byte("a"), EventTime: 1}},
		{{Value: []byte("b"), EventTime: 2}, {Value: []byte("c"), EventTime: 3}},
	}
	source := newMockSource(batches)

	cfg := DefaultTaskSlotConfig()
	cfg.WatermarkInterval = 50 * time.Millisecond

	// Source task: no input streams, operator chain is [noopMap, sink].
	ts := NewTaskSlot(cfg, nil, nil, []Operator{&noopMap{}, sink}, source)

	err := ts.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if sink.count.Load() != 3 {
		t.Fatalf("sink count: got %d, want 3", sink.count.Load())
	}
}

func TestTaskSlot_StreamThrough(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputWriter, outputReader, ts := newTestPipeline(t, []Operator{&noopMap{}}, nil)

	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx)
	}()

	// Write data to input.
	for i := 0; i < 5; i++ {
		err := inputWriter.WriteMessage(&protocol.DataRecordMsg{
			Key:       []byte("k"),
			Value:     []byte{byte(i)},
			EventTime: int64(i),
		})
		if err != nil {
			t.Fatalf("WriteMessage[%d]: %v", i, err)
		}
	}
	// Send EoP.
	inputWriter.WriteMessage(&protocol.EndOfPartitionMsg{
		SourceID: "test",
		Reason:   protocol.EndReasonExhausted,
	})

	// Read output events.
	var dataCount int
	var gotEnd bool
	for i := 0; i < 10; i++ {
		msg, err := outputReader.ReadMessage()
		if err != nil {
			break
		}
		switch msg.(type) {
		case *protocol.DataRecordMsg:
			dataCount++
		case *protocol.EndOfPartitionMsg:
			gotEnd = true
		}
		if gotEnd {
			break
		}
	}

	if dataCount != 5 {
		t.Errorf("data events: got %d, want 5", dataCount)
	}
	if !gotEnd {
		t.Error("expected EndOfPartition")
	}

	cancel()
	<-done
}

func TestTaskSlot_Shutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, ts := newTestPipeline(t, []Operator{&noopMap{}}, nil)

	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx)
	}()

	// Cancel context — all goroutines should exit within drain timeout.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Expect context cancellation error.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(ts.Config.DrainTimeout + 2*time.Second):
		t.Fatal("task slot did not shut down within timeout")
	}
}

func TestTaskSlot_CheckpointBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputWriter, outputReader, ts := newTestPipeline(t, []Operator{&noopMap{}}, nil)

	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx)
	}()

	// Write data, barrier, more data, then EoP.
	inputWriter.WriteMessage(&protocol.DataRecordMsg{Value: []byte("before"), EventTime: 1})
	inputWriter.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 42, EpochID: 7, Timestamp: 1000})
	inputWriter.WriteMessage(&protocol.DataRecordMsg{Value: []byte("after"), EventTime: 2})
	inputWriter.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "test", Reason: protocol.EndReasonExhausted})

	// Read output.
	var foundBarrier bool
	var dataCount int
readLoop:
	for i := 0; i < 10; i++ {
		msg, err := outputReader.ReadMessage()
		if err != nil {
			break
		}
		switch m := msg.(type) {
		case *protocol.DataRecordMsg:
			dataCount++
		case *protocol.CheckpointBarrierMsg:
			foundBarrier = true
			if m.CheckpointID != 42 {
				t.Errorf("checkpoint ID: got %d, want 42", m.CheckpointID)
			}
		case *protocol.EndOfPartitionMsg:
			_ = m
			break readLoop
		}
	}

	if dataCount != 2 {
		t.Errorf("data events: got %d, want 2", dataCount)
	}
	if !foundBarrier {
		t.Error("expected checkpoint barrier in output")
	}

	cancel()
	<-done
}

func TestTaskSlot_OperatorPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	inputWriter, _, ts := newTestPipeline(t, []Operator{&panicMap{limit: 2}}, nil)

	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx)
	}()

	// Send events — the panic map will panic on the 2nd event.
	inputWriter.WriteMessage(&protocol.DataRecordMsg{Value: []byte("1"), EventTime: 1})
	inputWriter.WriteMessage(&protocol.DataRecordMsg{Value: []byte("2"), EventTime: 2})

	select {
	case err := <-done:
		if !errors.Is(err, ErrOperatorPanic) {
			t.Fatalf("expected ErrOperatorPanic, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("task slot did not fail within timeout")
	}
}

func TestTaskSlot_BarrierAlignment(t *testing.T) {
	// Two-input task with barrier alignment.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := DefaultTaskSlotConfig()

	// Create two input stream pairs.
	iw0, ir0 := newTestStreamPair(t)
	iw1, ir1 := newTestStreamPair(t)
	ow, or := newTestStreamPair(t)

	inputs := []*transport.FrameStream{ir0, ir1}
	outputs := []*transport.FrameStream{ow}

	ts := NewTaskSlot(cfg, inputs, outputs, []Operator{&noopMap{}}, nil)

	done := make(chan error, 1)
	go func() {
		done <- ts.Run(ctx)
	}()

	// Input 0: data, barrier, data (side-buffered).
	iw0.WriteMessage(&protocol.DataRecordMsg{Value: []byte("i0-before"), EventTime: 1})
	iw0.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000})
	iw0.WriteMessage(&protocol.DataRecordMsg{Value: []byte("i0-after"), EventTime: 2})

	// Input 1: data, barrier.
	iw1.WriteMessage(&protocol.DataRecordMsg{Value: []byte("i1-before"), EventTime: 3})
	iw1.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000})

	// Both EoPs.
	iw0.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s0", Reason: protocol.EndReasonExhausted})
	iw1.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "s1", Reason: protocol.EndReasonExhausted})

	// Read output — should get:
	// i0-before, i1-before (in some order), barrier, i0-after (drained), EoP.
	var dataValues []string
	var foundBarrier bool
	var foundEnd bool
	for i := 0; i < 20; i++ {
		msg, err := or.ReadMessage()
		if err != nil {
			break
		}
		switch m := msg.(type) {
		case *protocol.DataRecordMsg:
			dataValues = append(dataValues, string(m.Value))
		case *protocol.CheckpointBarrierMsg:
			foundBarrier = true
		case *protocol.EndOfPartitionMsg:
			foundEnd = true
		}
		if foundEnd {
			break
		}
	}

	if len(dataValues) != 3 {
		t.Errorf("data events: got %d (%v), want 3", len(dataValues), dataValues)
	}
	if !foundBarrier {
		t.Error("expected checkpoint barrier in output")
	}
	if !foundEnd {
		t.Error("expected EndOfPartition in output")
	}

	cancel()
	<-done
}

func TestWatermarkEmitter_MonotonicAdvance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	source := newMockSource(nil)
	var wm atomic.Int64
	outputCh := make(chan OutputMsg, 100)

	source.SetWatermark(100)

	// Run emitter for a short time.
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	// Set watermark higher midway.
	go func() {
		time.Sleep(150 * time.Millisecond)
		source.SetWatermark(200)
	}()

	_ = runWatermarkEmitter(ctx, source, &wm, outputCh, 50*time.Millisecond, testLogger())

	close(outputCh)

	// Verify monotonic watermark values.
	var prev int64
	for msg := range outputCh {
		if msg.Type != OutputWatermark {
			continue
		}
		ts := msg.Watermark.Timestamp
		if ts < prev {
			t.Errorf("non-monotonic watermark: %d after %d", ts, prev)
		}
		prev = ts
	}

	if wm.Load() < 200 {
		t.Errorf("final watermark: got %d, want >= 200", wm.Load())
	}
}

func TestWatermarkEmitter_ConcurrentAccess(t *testing.T) {
	// This test is designed to be run with -race.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	source := newMockSource(nil)
	var wm atomic.Int64
	outputCh := make(chan OutputMsg, 1000)

	// Concurrent watermark updates from multiple goroutines.
	for i := 0; i < 5; i++ {
		go func(base int64) {
			for j := int64(0); j < 100; j++ {
				source.SetWatermark(base + j)
				time.Sleep(time.Millisecond)
			}
		}(int64(i * 1000))
	}

	_ = runWatermarkEmitter(ctx, source, &wm, outputCh, 10*time.Millisecond, testLogger())
	// No panic or race condition = success.
}
