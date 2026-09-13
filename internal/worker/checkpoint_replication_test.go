package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

type checkpointReplicaClientFunc func(context.Context, rpc.ReplicateCheckpointRequest, io.Reader) error

func (f checkpointReplicaClientFunc) ReplicateCheckpoint(ctx context.Context, r rpc.ReplicateCheckpointRequest, body io.Reader) error {
	return f(ctx, r, body)
}

func TestInlineCheckpointReplicationFencesExecution(t *testing.T) {
	snapshot := engine.TaskCheckpoint{TaskID: "task", CheckpointID: 7, EpochID: 2, HasSource: true, Source: []byte("offset"), Operators: [][]byte{[]byte("state")}}
	calls := 0
	sentinel := errors.New("replica refused publication")
	r := &inlineCheckpointReplicator{jobID: "job", taskID: "task", epoch: 2, client: checkpointReplicaClientFunc(func(_ context.Context, request rpc.ReplicateCheckpointRequest, body io.Reader) error {
		calls++
		payload, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		if request.JobID != "job" || request.TaskID != "task" || request.CheckpointID != 7 || request.EpochID != 2 || request.Size != uint64(len(payload)) || request.SHA256 != sha256.Sum256(payload) {
			t.Fatalf("request: %+v", request)
		}
		var decoded engine.TaskCheckpoint
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, snapshot) {
			t.Fatalf("snapshot changed: %+v", decoded)
		}
		return sentinel
	})}
	for _, epoch := range []uint64{1, 3} {
		wrong := snapshot
		wrong.EpochID = epoch
		if err := r.Replicate(context.Background(), wrong); err == nil {
			t.Fatal("wrong epoch accepted")
		}
	}
	wrong := snapshot
	wrong.TaskID = "other"
	if err := r.Replicate(context.Background(), wrong); err == nil {
		t.Fatal("wrong task accepted")
	}
	if calls != 0 {
		t.Fatal("invalid snapshot reached peer")
	}
	if err := r.Replicate(context.Background(), snapshot); !errors.Is(err, sentinel) {
		t.Fatalf("receipt error lost: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
