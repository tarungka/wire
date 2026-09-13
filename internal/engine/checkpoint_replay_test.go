package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestInputStreamDropsOnlyGloballyCompletedBarriers(t *testing.T) {
	writer, reader := newTestStreamPair(t)
	defer writer.Close()
	defer reader.Close()
	cc, _ := newTestCoordinator(CheckpointConfig{Timeout: time.Second}, 2)
	reader.SetCheckpointCompletionReader(cc.LastCompletedCheckpoint)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cc.Run(ctx) }()
	defer func() { cancel(); <-done }()
	if err := cc.TriggerCheckpoint(ctx, 7, 1); err != nil {
		t.Fatal(err)
	}
	cc.AckCheckpoint(0, 7)
	// A locally acknowledged checkpoint is not globally completed. Its barrier
	// must still be visible to the remaining downstream inputs.
	if cc.LastCompletedCheckpoint() != 0 {
		t.Fatal("partial ACK advanced completion")
	}
	if err := writer.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 1}); err != nil {
		t.Fatal(err)
	}
	msg, err := reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := msg.(*protocol.CheckpointBarrierMsg); !ok || b.CheckpointID != 7 {
		t.Fatalf("active barrier dropped: %v", msg)
	}
	cc.AckCheckpoint(1, 7)
	waitForNoActiveCheckpoint(t, cc, time.Second)
	if cc.LastCompletedCheckpoint() != 7 {
		t.Fatal("global ACK did not publish completion")
	}
	for _, id := range []uint64{6, 7, 8} {
		if err := writer.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: id, EpochID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err = reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := msg.(*protocol.CheckpointBarrierMsg); !ok || b.CheckpointID != 8 {
		t.Fatalf("completed barrier replayed: %v", msg)
	}
	// Recovery can restore a newer completion than this coordinator instance.
	reader.MarkCheckpointCompleted(10)
	reader.MarkCheckpointCompleted(9)
	for _, id := range []uint64{9, 10, 11} {
		if err := writer.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: id, EpochID: 2}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err = reader.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if b, ok := msg.(*protocol.CheckpointBarrierMsg); !ok || b.CheckpointID != 11 {
		t.Fatalf("restored barrier replayed: %v", msg)
	}
}
