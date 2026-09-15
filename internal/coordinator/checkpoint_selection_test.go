package coordinator

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

func TestRecoverySelectsEarlierValidManifest(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "unsupported", "invalid state"} {
		t.Run(mode, func(t *testing.T) {
			c, store := checkpointPolicyCoordinator(t)
			var latest uint64
			for i := 0; i < 2; i++ {
				cp, err := c.TriggerCheckpoint("job")
				if err != nil {
					t.Fatal(err)
				}
				err = c.AcknowledgeCheckpoint(rpc.AcknowledgeCheckpointRequest{JobID: "job", TaskID: "task", WorkerID: "worker", CheckpointID: cp.ID, EpochID: cp.EpochID, State: manifestState(t, "task", "replica")})
				if err != nil {
					t.Fatal(err)
				}
				latest = cp.ID
			}
			key := CheckpointManifestKey("job", latest)
			switch mode {
			case "missing":
				if err := store.Delete(key); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := store.Set(key, []byte("bad json")); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				if err := store.Set(key, []byte(`{"schema_version":99}`)); err != nil {
					t.Fatal(err)
				}
			case "invalid state":
				cp := CheckpointMeta{ID: latest, JobID: "job", Status: CheckpointCompleted, InvalidReason: "state missing", Timestamp: time.Now()}
				if err := store.Set(CheckpointKey("job", latest), encode(t, cp)); err != nil {
					t.Fatal(err)
				}
			}
			c.mu.Lock()
			selected, _, err := c.selectRecoveryCheckpointLocked(c.jobs["job"])
			c.mu.Unlock()
			if mode == "unsupported" {
				if !errors.Is(err, engine.ErrUnsupportedSchemaVersion) {
					t.Fatalf("upgrade error: %v", err)
				}
				if c.jobs["job"].LatestCheckpoint != latest {
					t.Fatal("unknown schema silently replaced")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if selected.ID != latest-1 || c.jobs["job"].LatestCheckpoint != latest-1 {
				t.Fatal("fallback did not select one older boundary")
			}
		})
	}
}

func TestRecoveryDoesNotReplacePinnedSavepoint(t *testing.T) {
	c, store := checkpointPolicyCoordinator(t)
	old := CheckpointMeta{ID: 1, JobID: "job", Status: CheckpointCompleted}
	pinned := CheckpointMeta{ID: 2, JobID: "job", Status: CheckpointCompleted, ManifestVersion: 1, SavepointID: "sp"}
	if err := store.Set(CheckpointKey("job", 1), encode(t, old)); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(CheckpointKey("job", 2), encode(t, pinned)); err != nil {
		t.Fatal(err)
	}
	job := c.jobs["job"]
	job.LatestCheckpoint = 2
	job.RescaleCheckpoint = 2
	c.mu.Lock()
	_, _, err := c.selectRecoveryCheckpointLocked(job)
	c.mu.Unlock()
	if err == nil || job.LatestCheckpoint != 2 {
		t.Fatal("missing pinned savepoint silently replaced")
	}
}

func TestRecoveryNeverSkipsPossibleCommittedSink(t *testing.T) {
	for _, reason := range []string{"missing archive", "corrupt manifest"} {
		t.Run(reason, func(t *testing.T) {
			c, store := checkpointPolicyCoordinator(t)
			old := CheckpointMeta{ID: 1, JobID: "job", Status: CheckpointCompleted}
			latest := CheckpointMeta{ID: 2, JobID: "job", Status: CheckpointCompleted, InvalidReason: reason,
				TaskManifests: map[string][]byte{}}
			// Use the production serializer so the transaction flag matches TaskMeta.
			raw, err := json.Marshal(engine.TaskMeta{TaskID: "sink", SinkPrepared: true})
			if err != nil {
				t.Fatal(err)
			}
			latest.TaskManifests["sink"] = raw
			if reason == "corrupt manifest" {
				latest.InvalidReason = ""
				latest.ManifestVersion = engine.CurrentSchemaVersion
				if err := store.Set(CheckpointManifestKey("job", 2), []byte("bad json")); err != nil {
					t.Fatal(err)
				}
			}
			for _, cp := range []CheckpointMeta{old, latest} {
				if err := store.Set(CheckpointKey("job", cp.ID), encode(t, cp)); err != nil {
					t.Fatal(err)
				}
			}
			job := c.jobs["job"]
			job.LatestCheckpoint = 2
			_, _, err = c.selectRecoveryCheckpointLocked(job)
			if !errors.Is(err, errNoValidCheckpoint) || job.LatestCheckpoint != 2 {
				t.Fatalf("unsafe fallback: checkpoint=%d error=%v", job.LatestCheckpoint, err)
			}
		})
	}
}
