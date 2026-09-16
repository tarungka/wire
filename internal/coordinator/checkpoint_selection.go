package coordinator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/protocol"
)

var errCheckpointStoreRead = errors.New("checkpoint metadata store read failed")

var errNoValidCheckpoint = errors.New("no valid completed checkpoint available for recovery")

func checkpointInventory(store MetadataStore, cp CheckpointMeta) (map[string]engine.TaskMeta, error) {
	inventory := make(map[string]engine.TaskMeta)
	if cp.InvalidReason != "" {
		return nil, fmt.Errorf("checkpoint invalid: %s", cp.InvalidReason)
	}
	if cp.ManifestVersion == 0 {
		return inventory, nil
	} // Pre-WIP-06 records retain legacy recovery.
	if cp.ManifestVersion != engine.CurrentSchemaVersion {
		return nil, engine.ErrUnsupportedSchemaVersion
	}
	raw, err := store.Get(CheckpointManifestKey(cp.JobID, cp.ID))
	if err != nil {
		return nil, errors.Join(errCheckpointStoreRead, err)
	}
	manifest, err := engine.UnmarshalCheckpointMetadata(raw)
	if err != nil {
		return nil, err
	}
	if err := manifest.ValidateComplete(); err != nil {
		return nil, err
	}
	if manifest.JobID != cp.JobID || manifest.CheckpointID <= 0 || uint64(manifest.CheckpointID) != cp.ID || manifest.JobGraph.NumKeyGroups != cp.NumKeyGroups {
		return nil, fmt.Errorf("recovery manifest identity mismatch")
	}
	for _, task := range manifest.Tasks {
		if cp.Tasks[task.TaskID] == "" || task.StateSHA256["checkpoint.archive"] == "" || task.StateSizeBytes <= 0 {
			return nil, fmt.Errorf("invalid archive inventory")
		}
		inventory[task.TaskID] = task
	}
	if len(inventory) != len(cp.Tasks) {
		return nil, fmt.Errorf("manifest task coverage mismatch")
	}
	// Validate the manifest against the inventory and topology captured in the
	// completion record, not only against its own internally consistent graph.
	recorded := make(map[string]engine.TaskMeta)
	for id, raw := range cp.TaskManifests {
		var task engine.TaskMeta
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, err
		}
		recorded[id] = task
	}
	expected, err := checkpointManifest(&JobMeta{ID: cp.JobID, Name: manifest.JobName}, cp, recorded, manifest.CompletionTime)
	if err != nil {
		return nil, err
	}
	sort.Slice(manifest.Tasks, func(i, j int) bool { return manifest.Tasks[i].TaskID < manifest.Tasks[j].TaskID })
	sort.Slice(manifest.JobGraph.Operators, func(i, j int) bool {
		return manifest.JobGraph.Operators[i].OperatorID < manifest.JobGraph.Operators[j].OperatorID
	})
	sort.Slice(manifest.SinkTxns, func(i, j int) bool { return manifest.SinkTxns[i].TaskID < manifest.SinkTxns[j].TaskID })
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return nil, err
	}
	actualJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(expectedJSON, actualJSON) {
		return nil, fmt.Errorf("manifest does not match completed task inventory")
	}
	return inventory, nil
}

// selectRecoveryCheckpointLocked chooses one boundary for the entire deployment.
// An explicit rescale savepoint is never silently replaced with another boundary.
func (c *Coordinator) selectRecoveryCheckpointLocked(job *JobMeta) (CheckpointMeta, map[string]engine.TaskMeta, error) {
	pinned := job.RescaleCheckpoint != 0 && job.RescaleCheckpoint == job.LatestCheckpoint
	var candidates []CheckpointMeta
	err := c.store.PrefixScan([]byte(fmt.Sprintf("jobs/%s/checkpoints/", job.ID)), func(key, value []byte) bool {
		if strings.HasSuffix(string(key), "/latest") || strings.HasSuffix(string(key), "/metadata.json") {
			return true
		}
		var cp CheckpointMeta
		if protocol.DecodeMsgPack(value, &cp) == nil && cp.JobID == job.ID && cp.ID <= job.LatestCheckpoint && cp.Status == CheckpointCompleted {
			candidates = append(candidates, cp)
		}
		return true
	})
	if err != nil {
		return CheckpointMeta{}, nil, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID > candidates[j].ID })
	for _, cp := range candidates {
		if pinned && cp.ID != job.RescaleCheckpoint {
			continue
		}
		inventory, err := checkpointInventory(c.store, cp)
		if errors.Is(err, engine.ErrUnsupportedSchemaVersion) || errors.Is(err, errCheckpointStoreRead) {
			return CheckpointMeta{}, nil, err
		}
		if err != nil {
			// Completion authorizes CommitCheckpoint; delivery is not acknowledged.
			// A prepared sink may therefore already have committed this boundary.
			for _, raw := range cp.TaskManifests {
				var task engine.TaskMeta
				if decodeErr := json.Unmarshal(raw, &task); decodeErr != nil || task.SinkPrepared {
					return CheckpointMeta{}, nil, fmt.Errorf("%w: cannot skip checkpoint %d with a possible committed sink transaction", errNoValidCheckpoint, cp.ID)
				}
			}
			if pinned {
				return CheckpointMeta{}, nil, err
			}
			c.log.Warn().Err(err).Uint64("checkpoint", cp.ID).Msg("skipping invalid recovery checkpoint")
			continue
		}
		if cp.ID != job.LatestCheckpoint {
			next := *job
			// LatestCheckpoint is the selected recovery boundary, not a high-water mark.
			next.LatestCheckpoint = cp.ID
			if err := c.persistJobLocked(&next); err != nil {
				return CheckpointMeta{}, nil, err
			}
			job.LatestCheckpoint = cp.ID
		}
		return cp, inventory, nil
	}
	return CheckpointMeta{}, nil, errNoValidCheckpoint
}
