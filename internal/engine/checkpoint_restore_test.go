package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

type checkpointRestoreProbe struct {
	noopMap
	opened bool
	state  string
}

func (p *checkpointRestoreProbe) Open(context.Context) error { p.opened = true; return nil }
func (p *checkpointRestoreProbe) RestoreCheckpoint(data []byte) error {
	if !p.opened {
		return fmt.Errorf("restore before open")
	}
	p.state = string(data)
	return nil
}
func (p *checkpointRestoreProbe) Map(_ context.Context, event Event) (Event, error) {
	if p.state == "" {
		return Event{}, fmt.Errorf("processing before restore")
	}
	event.Value = []byte(p.state)
	return event, nil
}

func TestTaskRestoresBeforeProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	probe := &checkpointRestoreProbe{}
	input, output, slot := newTestPipeline(t, []Operator{probe}, nil)
	slot.TaskID = "task"
	slot.RestoreCheckpoint = &TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, Operators: [][]byte{[]byte("restored")}}
	done := make(chan error, 1)
	go func() { done <- slot.Run(ctx) }()
	if err := input.WriteMessage(&protocol.DataRecordMsg{Value: []byte("input")}); err != nil {
		t.Fatal(err)
	}
	message, err := output.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	record, ok := message.(*protocol.DataRecordMsg)
	if !ok || string(record.Value) != "restored" {
		t.Fatalf("record: %+v", message)
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
	if slot.RestoredCheckpointID != 7 {
		t.Fatal("restored checkpoint ID not applied")
	}
}
