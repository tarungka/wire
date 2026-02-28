package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/transport"
)

// newTestStreamPair creates a connected pair of FrameStreams for testing.
// Returns (writer, reader) where writer sends and reader receives.
func newTestStreamPair(t *testing.T) (*transport.FrameStream, *transport.FrameStream) {
	t.Helper()

	sCfg := transport.DefaultConfig()
	sCfg.ListenAddr = "127.0.0.1:0"
	server := transport.NewMux(sCfg)

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("server Listen: %v", err)
	}
	addr := server.ListenAddr()

	cCfg := transport.DefaultConfig()
	client := transport.NewMux(cCfg)

	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	writer, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	reader, err := server.Accept(ctx)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if _, err := reader.ReceiveHandshake(); err != nil {
		t.Fatalf("ReceiveHandshake: %v", err)
	}

	return writer, reader
}

// testTracker creates a single-input InputWatermarkTracker for testing.
func testTracker(numInputs int) *InputWatermarkTracker {
	return NewInputWatermarkTracker(numInputs)
}

func TestInputReader_DataRecordRouting(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	// Write 3 data records then EoP.
	go func() {
		for i := 0; i < 3; i++ {
			writer.WriteMessage(&protocol.DataRecordMsg{
				Key:       []byte("key"),
				Value:     []byte{byte(i)},
				EventTime: int64(i * 1000),
			})
		}
		writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// Should have 3 events in eventCh.
	close(eventCh)
	var count int
	for e := range eventCh {
		if string(e.Key) != "key" {
			t.Errorf("event key: got %q, want %q", e.Key, "key")
		}
		count++
	}
	if count != 3 {
		t.Fatalf("got %d events, want 3", count)
	}

	// Should have 1 EoP control message.
	select {
	case ctrl := <-controlCh:
		if ctrl.Type != CtrlEndOfPartition {
			t.Errorf("expected CtrlEndOfPartition, got %v", ctrl.Type)
		}
	default:
		t.Fatal("expected EoP control message")
	}
}

func TestInputReader_BarrierDetection(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	go func() {
		writer.WriteMessage(&protocol.CheckpointBarrierMsg{
			CheckpointID: 42,
			EpochID:      7,
			Timestamp:    1000,
		})
		writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// Should have barrier + EoP in controlCh.
	ctrl1 := <-controlCh
	if ctrl1.Type != CtrlBarrierReceived {
		t.Errorf("expected CtrlBarrierReceived, got %v", ctrl1.Type)
	}
	if ctrl1.CheckpointID != 42 || ctrl1.EpochID != 7 {
		t.Errorf("barrier: checkpoint=%d epoch=%d, want 42/7", ctrl1.CheckpointID, ctrl1.EpochID)
	}

	ctrl2 := <-controlCh
	if ctrl2.Type != CtrlEndOfPartition {
		t.Errorf("expected CtrlEndOfPartition, got %v", ctrl2.Type)
	}
}

func TestInputReader_WatermarkCASUpdate(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	go func() {
		writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "s"})
		writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 200, SourceID: "s"})
		writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	if tracker.watermarks[0].Load() != 200 {
		t.Errorf("watermark: got %d, want 200", tracker.watermarks[0].Load())
	}
}

func TestInputReader_SideBufferRouting(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(2, 100) // 2 inputs for alignment.
	tracker := testTracker(2)

	go func() {
		// Send some data, then a barrier (this is input 0 with 2-input aligner).
		writer.WriteMessage(&protocol.DataRecordMsg{Value: []byte("before-barrier"), EventTime: 1})
		writer.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000})
		// After barrier, input 0 is aligning — data should go to side buffer.
		writer.WriteMessage(&protocol.DataRecordMsg{Value: []byte("after-barrier"), EventTime: 2})
		writer.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "test", Reason: protocol.EndReasonExhausted})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// "before-barrier" should be in eventCh.
	close(eventCh)
	var events []Event
	for e := range eventCh {
		events = append(events, e)
	}
	if len(events) != 1 || string(events[0].Value) != "before-barrier" {
		t.Fatalf("expected 1 event 'before-barrier', got %d events", len(events))
	}

	// "after-barrier" should be in the side buffer (input 0 was aligning).
	// We can verify by draining the aligner (need to trigger alignment first for input 1).
	aligner.OnBarrier(1, 1, 1)
	drained := aligner.DrainAll(1)
	if len(drained) != 1 || string(drained[0].Value) != "after-barrier" {
		t.Fatalf("expected 1 side-buffered event 'after-barrier', got %d events", len(drained))
	}
}

func TestInputReader_ContextCancellation(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer reader.Close()

	ctx, cancel := context.WithCancel(context.Background())

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	// Cancel context after a short delay and close the writer to produce
	// an EOF on the reader side, unblocking ReadMessage.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
		writer.Close() // Causes EOF on reader, unblocking ReadMessage.
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	// Should exit cleanly (nil) due to EOF / context cancellation.
	if err != nil && err != context.Canceled {
		t.Fatalf("expected nil or context.Canceled, got: %v", err)
	}
}

func TestInputReader_WatermarkDoesNotRegress(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)
	tracker.watermarks[0].Store(200) // Pre-set to a higher value.

	go func() {
		// Send a stale watermark (100 < 200).
		writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "s"})
		writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// Watermark should NOT have regressed.
	if tracker.watermarks[0].Load() != 200 {
		t.Errorf("watermark regressed: got %d, want 200", tracker.watermarks[0].Load())
	}
}

func TestInputReader_EventChannelFull_UnblocksOnContextCancel(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer reader.Close()

	ctx, cancel := context.WithCancel(context.Background())

	eventCh := make(chan Event, 1) // Tiny channel.
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	go func() {
		// Send enough data to fill eventCh and block the reader.
		for i := 0; i < 10; i++ {
			writer.WriteMessage(&protocol.DataRecordMsg{
				Value:     []byte{byte(i)},
				EventTime: int64(i),
			})
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	}()

	// Let the reader block on the full channel, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()
	writer.Close() // Unblock the inner read goroutine.

	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("expected nil or context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("input reader did not unblock on context cancel")
	}
}

func TestInputReader_ActivityRecording(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	// Verify no activity initially.
	if tracker.lastActivityNs[0].Load() != 0 {
		t.Fatal("expected no initial activity")
	}

	go func() {
		writer.WriteMessage(&protocol.DataRecordMsg{
			Value:     []byte("data"),
			EventTime: 1000,
		})
		writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		})
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// Activity should have been recorded.
	if tracker.lastActivityNs[0].Load() == 0 {
		t.Error("expected activity to be recorded after data record")
	}
}
