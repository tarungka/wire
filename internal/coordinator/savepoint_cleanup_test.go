package coordinator

import (
	"errors"
	"testing"

	"github.com/tarungka/wire/internal/protocol"
)

func TestSavepointDeletionPublishesCleanupAtomically(t *testing.T) {
	c, base := newReadyCoordinator(t)
	store := &deploymentBatchStore{MetadataStore: base, fail: true}
	c.store = store
	sp := SavepointMeta{JobID: "job", ID: "save", CheckpointID: 7, EpochID: 2, Status: SavepointCompleted}
	cp := CheckpointMeta{JobID: "job", ID: 7, EpochID: 2, SavepointID: "save", Status: CheckpointCompleted, Replicas: map[string]string{"task": "replica:1"}}
	if err := store.Set(SavepointKey("job", "save"), encode(t, sp)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(CheckpointKey("job", 7), encode(t, cp)); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSavepoint("job", "save"); err == nil {
		t.Fatal("failed batch accepted")
	}
	if _, err := c.GetSavepoint("job", "save"); err != nil {
		t.Fatal("failed batch hid savepoint", err)
	}
	raw, err := store.Get(savepointCleanupKey("job", "save"))
	if err != nil || len(raw) != 0 {
		t.Fatalf("failed batch published cleanup: %v", err)
	}
	store.fail = false
	if err := c.DeleteSavepoint("job", "save"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetSavepoint("job", "save"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("deleted savepoint visible: %v", err)
	}
	list, err := c.ListSavepoints("job")
	if err != nil || len(list) != 0 {
		t.Fatalf("deleted savepoint listed: %+v %v", list, err)
	}
	raw, err = store.Get(savepointCleanupKey("job", "save"))
	if err != nil {
		t.Fatal(err)
	}
	var cleanup SavepointCleanup
	if err := protocol.DecodeMsgPack(raw, &cleanup); err != nil {
		t.Fatal(err)
	}
	if cleanup.CheckpointID != 7 || cleanup.EpochID != 2 || cleanup.Replicas["task"] != "replica:1" || cleanup.RequestedAt.IsZero() {
		t.Fatalf("cleanup=%+v", cleanup)
	}
	raw, err = store.Get(CheckpointKey("job", 7))
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.DecodeMsgPack(raw, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.InvalidReason == "" || cp.Status != CheckpointCompleted {
		t.Fatalf("recovery/outcome fence=%+v", cp)
	}
	if err := c.DeleteSavepoint("job", "save"); !errors.Is(err, ErrSavepointNotFound) {
		t.Fatalf("duplicate deletion: %v", err)
	}
}
