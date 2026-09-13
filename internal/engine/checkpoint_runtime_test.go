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
func testTaskCheckpointUpload(t *testing.T, fail bool, typed ...bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var operator Operator = &noopMap{}
	if len(typed) > 0 && typed[0] {
		operator = &typedCheckpointProbe{}
	}
	input, output, slot := newTestPipeline(t, []Operator{operator}, nil)
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
		if len(typed) > 0 && typed[0] {
			if len(s.StateHandleIndexes) != 1 || s.StateHandleIndexes[0] != 0 {
				t.Fatalf("typed handle marker lost: %+v", s)
			}
			if err := s.ValidateStateHandles(); err != nil {
				t.Fatal(err)
			}
		}
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

type triggeringCheckpointSource struct {
	*checkpointBoundarySource
	triggers chan CheckpointTrigger
}

func (s *triggeringCheckpointSource) ReadBatch(ctx context.Context) ([]Event, error) {
	batch, err := s.checkpointBoundarySource.ReadBatch(ctx)
	s.mu.Lock()
	first := s.batchIdx == 1 && batch != nil
	s.mu.Unlock()
	if first {
		s.triggers <- CheckpointTrigger{CheckpointID: 7, EpochID: 2}
	}
	return batch, err
}

func TestSourceTaskReplicatesBoundaryAndContinues(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			triggers := make(chan CheckpointTrigger, 1)
			source := &triggeringCheckpointSource{checkpointBoundarySource: &checkpointBoundarySource{newMockSource([][]Event{{{Value: []byte("before")}}, {{Value: []byte("after")}}})}, triggers: triggers}
			_, output, slot := newTestPipeline(t, []Operator{&noopMap{}}, source)
			slot.Config.WatermarkInterval = time.Hour
			cc, _ := newTestCoordinator(CheckpointConfig{Timeout: 2 * time.Second}, 1)
			slot.Coordinator = cc
			slot.TaskID = "source"
			slot.CheckpointTriggers = triggers
			started := make(chan TaskCheckpoint, 1)
			release := make(chan struct{})
			slot.CheckpointReplicator = checkpointReplicatorFunc(func(ctx context.Context, snapshot TaskCheckpoint) error {
				started <- snapshot
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
			if err := cc.TriggerCheckpoint(ctx, 7, 2); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- slot.Run(ctx) }()
			var snapshot TaskCheckpoint
			select {
			case snapshot = <-started:
			case <-ctx.Done():
				t.Fatal("source upload missing")
			}
			if !snapshot.HasSource || len(snapshot.Source) != 1 || snapshot.Source[0] != 1 || len(snapshot.Operators) != 1 {
				t.Fatalf("wrong snapshot: %+v", snapshot)
			}
			for i := 0; i < 3; i++ {
				msg, err := output.ReadMessage()
				if err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					barrier, ok := msg.(*protocol.CheckpointBarrierMsg)
					if !ok || barrier.CheckpointID != 7 || barrier.EpochID != 2 {
						t.Fatalf("barrier: %+v", msg)
					}
				} else {
					want := "before"
					if i == 2 {
						want = "after"
					}
					data, ok := msg.(*protocol.DataRecordMsg)
					if !ok || string(data.Value) != want {
						t.Fatalf("record %d: %+v", i, msg)
					}
				}
			}
			if cc.LastCompletedCheckpoint() != 0 {
				t.Fatal("source ACK before replication")
			}
			select {
			case err := <-done:
				t.Fatalf("source finished before upload: %v", err)
			default:
			}
			close(release)
			msg, err := output.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := msg.(*protocol.EndOfPartitionMsg); !ok {
				t.Fatalf("end: %T", msg)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("source failed to finish")
			}
			want := uint64(7)
			if fail {
				want = 0
			}
			if cc.LastCompletedCheckpoint() != want {
				t.Fatalf("completion=%d want=%d", cc.LastCompletedCheckpoint(), want)
			}
		})
	}
}

type typedCheckpointProbe struct{ noopMap }

func (*typedCheckpointProbe) Checkpoint(uint64) ([]byte, error) {
	panic("opaque checkpoint must not be called for typed state")
}
func (*typedCheckpointProbe) CheckpointState(id uint64) (SnapshotHandle, error) {
	return SnapshotHandle{CheckpointID: id, BackendType: StateBackendHashMap, Data: []byte("state")}, nil
}
func (*typedCheckpointProbe) RestoreState(SnapshotHandle) error { return nil }
func TestTaskCapturesTypedCheckpoint(t *testing.T)              { testTaskCheckpointUpload(t, false, true) }

func TestTaskReportsCheckpointToExternalCoordinator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input, output, slot := newTestPipeline(t, []Operator{&noopMap{}}, nil)
	slot.TaskID = "task"
	reported := make(chan uint64, 1)
	slot.CheckpointReplicator = checkpointReplicatorFunc(func(context.Context, TaskCheckpoint) error { return nil })
	slot.CheckpointReport = func(_ context.Context, id, epoch uint64, err error) error {
		if epoch != 2 || err != nil {
			return errors.New("unexpected checkpoint report")
		}
		reported <- id
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	if err := input.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-reported:
		if id != 7 {
			t.Fatalf("reported %d", id)
		}
	case <-ctx.Done():
		t.Fatal("external coordinator received no report")
	}
	if err := input.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("task did not finish")
	}
}

func TestExternalCheckpointAbortReleasesFailedUpload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	input, output, slot := newTestPipeline(t, []Operator{&noopMap{}}, nil)
	slot.TaskID = "task"
	decisions := make(chan ControlMsg, 1)
	slot.CheckpointDecisions = decisions
	uploadErr := errors.New("replica unavailable")
	slot.CheckpointReplicator = checkpointReplicatorFunc(func(context.Context, TaskCheckpoint) error { return uploadErr })
	reported := make(chan struct{})
	slot.CheckpointReport = func(ctx context.Context, id, epoch uint64, err error) error {
		if !errors.Is(err, uploadErr) {
			return errors.New("upload failure was lost")
		}
		select {
		case decisions <- ControlMsg{Type: CtrlAbortCheckpoint, CheckpointID: id, EpochID: epoch}:
			close(reported)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	if err := input.WriteMessage(&protocol.CheckpointBarrierMsg{CheckpointID: 7, EpochID: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reported:
	case <-ctx.Done():
		t.Fatal("failed upload was not reported")
	}
	if err := input.WriteMessage(&protocol.EndOfPartitionMsg{SourceID: "source"}); err != nil {
		t.Fatal(err)
	}
	if _, err := output.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("external abort did not release task")
	}
}
