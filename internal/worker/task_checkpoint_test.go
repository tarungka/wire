package worker

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCheckpointCommandFencingAndAbortOrder(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	w.epoch = 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := &taskCheckpointRuntime{source: true, triggers: make(chan engine.CheckpointTrigger, 1), decisions: make(chan engine.ControlMsg, 16)}
	w.tasks["task"] = &taskHandle{jobID: "job", epoch: 5, cancel: cancel, checkpoint: runtime}
	for _, epoch := range []uint64{4, 5} {
		data, err := protocol.EncodeMsgPack(rpc.TriggerCheckpointRequest{JobID: "job", CheckpointID: 7, EpochID: epoch})
		if err != nil {
			t.Fatal(err)
		}
		w.handleCheckpointCommand(rpc.WorkerCommand{Type: rpc.CommandTypeAbortCheckpoint, JobID: "job", TaskID: "task", Data: data})
		if epoch == 4 && len(runtime.decisions) != 0 {
			t.Fatal("stale decision admitted")
		}
	}
	for _, kind := range []engine.ControlType{engine.CtrlAbortTransaction, engine.CtrlAbortCheckpoint} {
		select {
		case decision := <-runtime.decisions:
			if decision.Type != kind || decision.CheckpointID != 7 || decision.EpochID != 5 {
				t.Fatalf("decision: %+v", decision)
			}
		default:
			t.Fatal("missing abort decision")
		}
	}
	if ctx.Err() != nil {
		t.Fatal("valid abort cancelled task")
	}
}
