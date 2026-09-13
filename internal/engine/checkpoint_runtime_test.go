package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestTaskCheckpointUploadDoesNotBlockProcessingOrAckEarly(t *testing.T) {
	testTaskCheckpointUpload(t, false)
}
func TestTaskCheckpointUploadFailureAbortsBeforeEOF(t *testing.T) {
	testTaskCheckpointUpload(t, true)
}
func testTaskCheckpointUpload(t *testing.T, fail bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input, output, slot := newTestPipeline(t, []Operator{&noopMap{}}, nil)
	cc, _ := newTestCoordinator(CheckpointConfig{Timeout: 2 * time.Second}, 1)
	slot.Coordinator = cc
	slot.TaskID = "task"
	started, release := make(chan TaskCheckpoint, 1), make(chan struct{})
	slot.CheckpointReplicator = checkpointReplicatorFunc(func(ctx context.Context, s TaskCheckpoint) error {
		started <- s
		select {
		case <-release:
			if fail {
				return errors.New("replica failed")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	if err := cc.TriggerCheckpoint(ctx, 7, 2); err != nil {
		t.Fatal(err)
	}
	if err := input.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 2}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-started:
		if s.CheckpointID != 7 || s.TaskID != "task" {
			t.Fatalf("snapshot: %+v", s)
		}
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	if err := input.WriteMessage(&protocol.DataRecordMsg{Value: []byte("during-upload")}); err != nil {
		t.Fatal(err)
	}
	// The barrier and post-barrier record must arrive while replication waits.
	for i := 0; i < 2; i++ {
		msg, err := output.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, ok := msg.(*protocol.CheckpointBarrierMsg); !ok {
				t.Fatalf("first: %T", msg)
			}
		} else {
			record, ok := msg.(*protocol.DataRecordMsg)
			if !ok || string(record.Value) != "during-upload" {
				t.Fatalf("record: %+v", msg)
			}
		}
	}
	if cc.LastCompletedCheckpoint() != 0 {
		t.Fatal("checkpoint ACK preceded durable replication")
	}
	if err := input.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "source"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatalf("task exited with upload pending: %v", err)
	default:
	}
	close(release)
	msg, err := output.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := msg.(*protocol.EndOfPartitionMsg); !ok {
		t.Fatalf("termination: %T", msg)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("task did not finish after upload")
	}
	if fail {
		cc.mu.Lock()
		failures := cc.totalFailures
		cc.mu.Unlock()
		if cc.LastCompletedCheckpoint() != 0 || failures != 1 {
			t.Fatalf("failed upload completed or abort lost: failures=%d", failures)
		}
	} else if cc.LastCompletedCheckpoint() != 7 {
		t.Fatal("durable completion was lost during task shutdown")
	}
}
