package coordinator

import (
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func TestCheckpointInspectionPersistedOutcomes(t *testing.T) {
	c, store := newTestCoordinator(t)
	start := time.Unix(100, 0)
	checkpoints := []CheckpointMeta{
		{ID: 1, JobID: "job", Status: CheckpointCompleted, Timestamp: start, CompletedAt: start.Add(1250 * time.Millisecond)},
		{ID: 2, JobID: "job", Status: CheckpointAborted, SavepointID: "savepoint"},
		{ID: 3, JobID: "job", Status: CheckpointFailed},
		{ID: 4, JobID: "job", Status: CheckpointInProgress},
	}
	for _, cp := range checkpoints {
		raw, err := protocol.EncodeMsgPack(cp)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(CheckpointKey("job", cp.ID), raw); err != nil {
			t.Fatal(err)
		}
	}
	// Manifest and latest-pointer records share the prefix but aren't outcomes.
	if err := store.Set(CheckpointManifestKey("job", 1), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(LatestCheckpointKey("job"), []byte("pointer")); err != nil {
		t.Fatal(err)
	}
	summary, err := c.checkpointInspection("job")
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalCompleted != 1 || summary.TotalFailed != 2 || summary.InProgress != 1 || summary.LatestCompleted != 1 || summary.LatestDurationMs == nil || *summary.LatestDurationMs != 1250 {
		t.Fatalf("summary=%+v", summary)
	}
	// Legacy records have no completion time: omit duration rather than invent it.
	raw, err := protocol.EncodeMsgPack(CheckpointMeta{ID: 5, JobID: "job", Status: CheckpointCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(CheckpointKey("job", 5), raw); err != nil {
		t.Fatal(err)
	}
	summary, err = c.checkpointInspection("job")
	if err != nil || summary.LatestCompleted != 5 || summary.LatestDurationMs != nil {
		t.Fatalf("legacy summary=%+v err=%v", summary, err)
	}
	if err := store.Set(CheckpointKey("job", 6), []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.checkpointInspection("job"); err == nil {
		t.Fatal("corrupt history silently omitted")
	}
}
