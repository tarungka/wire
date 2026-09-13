package engine

import (
	"context"
	"io"
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
		_ = client.Close()
		_ = server.Close()
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

func TestInputReader_DataRecordRouting(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	// Write 3 data records then EoP.
	go func() {
		for i := 0; i < 3; i++ {
			if err := writer.WriteMessage(&protocol.DataRecordMsg{
				Key:       []byte("key"),
				Value:     []byte{byte(i)},
				EventTime: int64(i * 1000),
			}); err != nil {
				t.Errorf("WriteMessage data record: %v", err)
			}
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
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
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	go func() {
		if err := writer.WriteMessage(&protocol.CheckpointBarrierMsg{
			CheckpointID: 42,
			EpochID:      7,
			Timestamp:    1000,
		}); err != nil {
			t.Errorf("WriteMessage barrier: %v", err)
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
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
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)

	go func() {
		if err := writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "s"}); err != nil {
			t.Errorf("WriteMessage watermark: %v", err)
		}
		if err := writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 200, SourceID: "s"}); err != nil {
			t.Errorf("WriteMessage watermark: %v", err)
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
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
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(2, 100) // 2 inputs for alignment.
	tracker := testTracker(2)

	go func() {
		// Send some data, then a barrier (this is input 0 with 2-input aligner).
		if err := writer.WriteMessage(&protocol.DataRecordMsg{Value: []byte("before-barrier"), EventTime: 1}); err != nil {
			t.Errorf("WriteMessage data record: %v", err)
		}
		if err := writer.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 1, EpochID: 1, Timestamp: 1000}); err != nil {
			t.Errorf("WriteMessage barrier: %v", err)
		}
		// After barrier, input 0 is aligning — data should go to side buffer.
		if err := writer.WriteMessage(&protocol.DataRecordMsg{Value: []byte("after-barrier"), EventTime: 2}); err != nil {
			t.Errorf("WriteMessage data record: %v", err)
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "test", Reason: protocol.EndReasonExhausted}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
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
	defer func() { _ = reader.Close() }()

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
		_ = writer.Close() // Causes EOF on reader, unblocking ReadMessage.
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	// Should exit cleanly (nil) due to EOF / context cancellation.
	if err != nil && err != context.Canceled {
		t.Fatalf("expected nil or context.Canceled, got: %v", err)
	}
}

func TestInputReader_WatermarkDoesNotRegress(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	tracker := testTracker(1)
	tracker.watermarks[0].Store(200) // Pre-set to a higher value.

	go func() {
		// Send a stale watermark (100 < 200).
		if err := writer.WriteMessage(&protocol.WatermarkMsg{Timestamp: 100, SourceID: "s"}); err != nil {
			t.Errorf("WriteMessage watermark: %v", err)
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
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
	defer writer.Close()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventCh := make(chan Event, 1)
	controlCh := make(chan ControlMsg, 10)
	sent := make(chan error, 1)
	go func() {
		for i := 0; i < 10; i++ {
			if err := writer.WriteMessageContext(ctx, &protocol.DataRecordMsg{Value: []byte{byte(i)}, EventTime: int64(i)}); err != nil {
				if ctx.Err() != nil {
					sent <- nil
				} else {
					sent <- err
				}
				return
			}
		}
		sent <- nil
	}()
	done := make(chan error, 1)
	go func() {
		done <- runInputReader(ctx, 0, reader, eventCh, controlCh, NewBarrierAligner(1, 100), testTracker(1), testLogger())
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for len(eventCh) == 0 {
		select {
		case err := <-done:
			t.Fatalf("reader exited before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("reader did not fill event channel")
		case <-tick.C:
		}
	}
	// No consumer drains the full channel. Cancellation must stop the receiver
	// and any sender paused by its buffer, without an external stream close.
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("reader cancellation: %v", err)
		}
	case <-deadline.C:
		t.Fatal("input reader did not stop on cancellation")
	}
	select {
	case err := <-sent:
		if err != nil {
			t.Fatalf("sender failed before cancellation: %v", err)
		}
	case <-deadline.C:
		t.Fatal("sender did not stop on cancellation")
	}
}

func TestInputReader_ActivityRecording(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer func() { _ = writer.Close() }()
	defer func() { _ = reader.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh := make(chan Event, 10)
	controlCh := make(chan ControlMsg, 10)
	aligner := NewBarrierAligner(1, 100)
	var now int64 = 100
	tracker := newInputWatermarkTracker(1, func() int64 { return now })
	now = 200

	go func() {
		if err := writer.WriteMessage(&protocol.DataRecordMsg{
			Value:     []byte("data"),
			EventTime: 1000,
		}); err != nil {
			t.Errorf("WriteMessage data record: %v", err)
		}
		if err := writer.WriteMessage(&protocol.EndOfPartitionMsg{
			SourceID: "test",
			Reason:   protocol.EndReasonExhausted,
		}); err != nil {
			t.Errorf("WriteMessage EoP: %v", err)
		}
	}()

	err := runInputReader(ctx, 0, reader, eventCh, controlCh, aligner, tracker, testLogger())
	if err != nil {
		t.Fatalf("runInputReader: %v", err)
	}

	// Activity should have been recorded.
	if tracker.lastActivityNs[0].Load() != 200 {
		t.Error("expected activity to be recorded after data record")
	}
}

func TestInputReaderUnexpectedEOFDoesNotLeaveTaskWaiting(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer reader.Close()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := runInputReader(ctx, 0, reader, make(chan Event, 1), make(chan ControlMsg, 1), NewBarrierAligner(1, 1), testTracker(1), testLogger())
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("closed input without EndOfPartition: %v", err)
	}
}
