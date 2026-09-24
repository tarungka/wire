package worker

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestShutdownStopsTaskAdmission(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	replicaClosed := false
	w.closeReplica = func() { replicaClosed = true }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.tasks["existing"] = &taskHandle{cancel: cancel}
	if err := w.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !replicaClosed {
		t.Fatal("replica endpoint was not closed")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("existing task was not cancelled")
	}
	// Even malformed late commands must be ignored before descriptor decoding or
	// status RPCs, since the coordinator transport has already been closed.
	w.handleDeployTask(rpc.WorkerCommand{TaskID: "late", JobID: "job", Data: []byte{0xff}})
	if _, ok := w.tasks["late"]; ok {
		t.Fatal("task admitted after shutdown")
	}
}

func TestDeploymentRejectsDifferentEpoch(t *testing.T) {
	w := New(Config{}, zerolog.Nop())
	w.epoch = 5
	for _, epoch := range []uint64{4, 6} {
		data, err := protocol.EncodeMsgPack(rpc.TaskDescriptor{EpochID: epoch})
		if err != nil {
			t.Fatal(err)
		}
		w.handleDeployTask(rpc.WorkerCommand{TaskID: "task", JobID: "job", Data: data})
		if len(w.tasks) != 0 {
			t.Fatal("deployment with mismatched epoch admitted")
		}
	}
}
