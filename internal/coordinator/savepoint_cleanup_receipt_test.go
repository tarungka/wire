package coordinator

import (
	"context"
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

type cleanupReceiptStore struct {
	MetadataStore
	fail bool
}

func (s *cleanupReceiptStore) Set(key, value []byte) error {
	if s.fail {
		return errors.New("receipt write failed")
	}
	return s.MetadataStore.Set(key, value)
}

func TestCleanupReceiptsFenceIdentityAndPersistProgress(t *testing.T) {
	for _, invalid := range []string{"", "term", "snapshot", "task", "worker", "checkpoint", "job", "savepoint", "lost", "removed"} {
		t.Run(invalid, func(t *testing.T) {
			c, base := newTestCoordinator(t)
			store := &cleanupReceiptStore{MetadataStore: base}
			c.store = store
			worker := &WorkerMeta{ID: "replica", CheckpointAddress: "replica:1"}
			c.workers[worker.ID] = worker
			cleanup := SavepointCleanup{JobID: "job", SavepointID: "save", CheckpointID: 7, EpochID: 2, Replicas: map[string]string{"a": "replica:1", "b": "replica:1"}}
			if err := store.Set(savepointCleanupKey("job", "save"), encode(t, cleanup)); err != nil {
				t.Fatal(err)
			}
			req := rpc.CheckpointCleanupRequest{WorkerID: "replica", EpochID: c.epoch, JobID: "job", SavepointID: "save", TaskID: "a", CheckpointID: 7, SnapshotEpoch: 2}
			switch invalid {
			case "term":
				req.EpochID++
			case "snapshot":
				req.SnapshotEpoch++
			case "task":
				req.TaskID = "other"
			case "worker":
				req.WorkerID = "other"
			case "checkpoint":
				req.CheckpointID++
			case "job":
				req.JobID = "other"
			case "savepoint":
				req.SavepointID = "other"
			case "lost":
				worker.Lost = true
			case "removed":
				worker.Removed = true
			}
			if invalid == "" {
				store.fail = true
				if _, err := c.HandleAcknowledgeCheckpointCleanup(context.Background(), 1, encode(t, req)); err == nil {
					t.Fatal("failed persistence accepted")
				}
				store.fail = false
			}
			response, rpcErr := c.HandleAcknowledgeCheckpointCleanup(context.Background(), 1, encode(t, req))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			if response.(*rpc.AcknowledgeCheckpointResponse).Accepted != (invalid == "") {
				t.Fatalf("response=%+v", response)
			}
			raw, err := store.Get(savepointCleanupKey("job", "save"))
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.DecodeMsgPack(raw, &cleanup); err != nil {
				t.Fatal(err)
			}
			if invalid != "" {
				if len(cleanup.Completed) != 0 {
					t.Fatal("invalid receipt changed progress")
				}
				return
			}
			if !cleanup.Completed["a"] || !cleanup.CompletedAt.IsZero() {
				t.Fatal("partial receipt finished cleanup")
			}
			req.TaskID = "b"
			for range 2 {
				response, rpcErr = c.HandleAcknowledgeCheckpointCleanup(context.Background(), 1, encode(t, req))
				if rpcErr != nil || !response.(*rpc.AcknowledgeCheckpointResponse).Accepted {
					t.Fatal("receipt retry failed", rpcErr)
				}
			}
			raw, err = store.Get(savepointCleanupKey("job", "save"))
			if err != nil {
				t.Fatal(err)
			}
			if err := protocol.DecodeMsgPack(raw, &cleanup); err != nil {
				t.Fatal(err)
			}
			if cleanup.CompletedAt.IsZero() || len(cleanup.Completed) != 2 {
				t.Fatalf("completion=%+v", cleanup)
			}
		})
	}
}
