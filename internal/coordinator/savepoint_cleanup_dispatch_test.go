package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestCleanupDispatchRetriesUntilDurableReceipt(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.jobs["job"] = &JobMeta{ID: "job", Status: JobCanceled}
	c.workers["replica"] = &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1", LastHeartbeat: time.Now()}
	cleanup := SavepointCleanup{JobID: "job", SavepointID: "save", CheckpointID: 7, EpochID: 2, Replicas: map[string]string{"task": "replica:1"}}
	if err := store.Set(savepointCleanupKey("job", "save"), encode(t, cleanup)); err != nil {
		t.Fatal(err)
	}
	c.dispatchSavepointCleanup(t.Context())
	c.dispatchSavepointCleanup(t.Context())
	commands := c.DrainCommands("replica")
	if len(commands) != 1 || commands[0].Type != rpc.CommandTypeDeleteCheckpoint {
		t.Fatalf("commands=%+v", commands)
	}
	// Delivery alone does not clear durable work.
	c.dispatchSavepointCleanup(t.Context())
	commands = c.DrainCommands("replica")
	if len(commands) != 1 {
		t.Fatal("unacknowledged command not retried")
	}
	var request rpc.CheckpointCleanupRequest
	if err := protocol.DecodeMsgPack(commands[0].Data, &request); err != nil {
		t.Fatal(err)
	}
	if request.SnapshotEpoch != 2 || request.EpochID != c.epoch {
		t.Fatalf("request=%+v", request)
	}
	response, err := c.HandleAcknowledgeCheckpointCleanup(t.Context(), 1, commands[0].Data)
	if err != nil || !response.(*rpc.AcknowledgeCheckpointResponse).Accepted {
		t.Fatal("receipt failed", err)
	}
	c.dispatchSavepointCleanup(t.Context())
	if len(c.DrainCommands("replica")) != 0 {
		t.Fatal("completed deletion dispatched again")
	}
}

func TestCleanupDispatchRecoversWithoutInMemoryQueue(t *testing.T) {
	c, store := newTestCoordinator(t)
	if err := c.persistJob(&JobMeta{ID: "job", Status: JobCanceled}); err != nil {
		t.Fatal(err)
	}
	if err := c.persistWorker(&WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}); err != nil {
		t.Fatal(err)
	}
	cleanup := SavepointCleanup{JobID: "job", SavepointID: "save", CheckpointID: 7, EpochID: 2, Replicas: map[string]string{"done": "replica:1", "pending": "replica:1"}, Completed: map[string]bool{"done": true}}
	if err := store.Set(savepointCleanupKey("job", "save"), encode(t, cleanup)); err != nil {
		t.Fatal(err)
	}
	recovered := New(c.config, store, nil, c.log)
	recovered.state = StateLeader
	recovered.epoch = c.epoch + 1
	if err := recovered.recover(); err != nil {
		t.Fatal(err)
	}
	recovered.dispatchSavepointCleanup(t.Context())
	if len(recovered.DrainCommands("replica")) != 0 {
		t.Fatal("dispatched before recovered worker renewed contact")
	}
	recovered.workers["replica"].LastHeartbeat = time.Now()
	recovered.workers["replica"].Lost = false
	recovered.dispatchSavepointCleanup(t.Context())
	commands := recovered.DrainCommands("replica")
	if len(commands) != 1 || commands[0].TaskID != "pending" || commands[0].EpochID != recovered.epoch {
		t.Fatalf("recovered commands=%+v", commands)
	}
}
