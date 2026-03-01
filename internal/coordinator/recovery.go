package coordinator

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/tarungka/wire/internal/protocol"
)

// recoveredState contains all state reconstructed from the metadata store
// during crash recovery.
type recoveredState struct {
	jobs               map[string]*JobMeta
	workers            map[string]*WorkerMeta
	epoch              uint64
	config             *ClusterConfig
	latestCheckpoints  map[string]*CheckpointMeta // jobID → latest completed
	checkpointsToAbort []*CheckpointMeta          // in-flight checkpoints to abort
}

// recoverFromStore reconstructs coordinator state from the metadata store.
// All workers are marked stale (heartbeat zeroed). The epoch is incremented
// and persisted to fence stale coordinators.
func recoverFromStore(store MetadataStore) (*recoveredState, error) {
	state := &recoveredState{
		jobs:              make(map[string]*JobMeta),
		workers:           make(map[string]*WorkerMeta),
		latestCheckpoints: make(map[string]*CheckpointMeta),
	}

	// 1. Recover jobs.
	var decodeErr error
	err := store.PrefixScan([]byte(JobsPrefix), func(key, value []byte) bool {
		k := string(key)

		// Only process job meta keys (jobs/{id}/meta).
		if !strings.HasSuffix(k, "/meta") {
			return true
		}
		// Extract job ID from "jobs/{id}/meta".
		parts := strings.Split(k, "/")
		if len(parts) != 3 {
			return true
		}
		jobID := parts[1]

		var job JobMeta
		if err := protocol.DecodeMsgPack(value, &job); err != nil {
			decodeErr = fmt.Errorf("%w: corrupt job entry %q: %v", ErrStoreCorrupted, k, err)
			return false
		}
		state.jobs[jobID] = &job
		return true
	})
	if decodeErr != nil {
		return nil, decodeErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: scanning jobs: %v", ErrRecoveryFailed, err)
	}

	// 2. Recover checkpoints for each job.
	for jobID := range state.jobs {
		if err := recoverJobCheckpoints(store, jobID, state); err != nil {
			return nil, err
		}
	}

	// 3. Recover workers (mark all as stale).
	decodeErr = nil
	err = store.PrefixScan([]byte(WorkersPrefix), func(key, value []byte) bool {
		k := string(key)
		if !strings.HasSuffix(k, "/meta") {
			return true
		}
		parts := strings.Split(k, "/")
		if len(parts) != 3 {
			return true
		}
		workerID := parts[1]

		var worker WorkerMeta
		if err := protocol.DecodeMsgPack(value, &worker); err != nil {
			decodeErr = fmt.Errorf("%w: corrupt worker entry %q: %v", ErrStoreCorrupted, k, err)
			return false
		}
		// Mark worker as stale by zeroing heartbeat.
		worker.LastHeartbeat = worker.LastHeartbeat.UTC()
		state.workers[workerID] = &worker
		return true
	})
	if decodeErr != nil {
		return nil, decodeErr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: scanning workers: %v", ErrRecoveryFailed, err)
	}

	// 4. Read current epoch.
	epochData, err := store.Get(ClusterEpochKey())
	if err != nil {
		return nil, fmt.Errorf("%w: reading epoch: %v", ErrRecoveryFailed, err)
	}
	if len(epochData) == 8 {
		state.epoch = binary.BigEndian.Uint64(epochData)
	}

	// 5. Read cluster config.
	cfgData, err := store.Get(ClusterConfigKey())
	if err != nil {
		return nil, fmt.Errorf("%w: reading config: %v", ErrRecoveryFailed, err)
	}
	if cfgData != nil {
		var cfg ClusterConfig
		if err := protocol.DecodeMsgPack(cfgData, &cfg); err == nil {
			state.config = &cfg
		}
	}

	// 6. Increment epoch and persist (fence stale coordinators).
	state.epoch++
	epochBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBuf, state.epoch)
	if err := store.Set(ClusterEpochKey(), epochBuf); err != nil {
		return nil, fmt.Errorf("%w: persisting new epoch: %v", ErrRecoveryFailed, err)
	}

	return state, nil
}

// recoverJobCheckpoints scans checkpoints for a job and identifies
// the latest completed one and any in-flight checkpoints that need aborting.
func recoverJobCheckpoints(store MetadataStore, jobID string, state *recoveredState) error {
	prefix := fmt.Appendf(nil, "jobs/%s/checkpoints/", jobID)

	var latestCompleted *CheckpointMeta
	var decodeErr error
	err := store.PrefixScan(prefix, func(key, value []byte) bool {
		k := string(key)
		// Skip the "latest" pointer key.
		if strings.HasSuffix(k, "/latest") {
			return true
		}

		var cp CheckpointMeta
		if err := protocol.DecodeMsgPack(value, &cp); err != nil {
			decodeErr = fmt.Errorf("%w: corrupt checkpoint entry %q: %v", ErrStoreCorrupted, k, err)
			return false
		}

		switch cp.Status {
		case CheckpointCompleted:
			if latestCompleted == nil || cp.ID > latestCompleted.ID {
				latestCompleted = &cp
			}
		case CheckpointTriggered, CheckpointInProgress:
			// In-flight checkpoint: mark for abort.
			cp.Status = CheckpointAborted
			state.checkpointsToAbort = append(state.checkpointsToAbort, &cp)
		}
		return true
	})
	if decodeErr != nil {
		return decodeErr
	}
	if err != nil {
		return fmt.Errorf("%w: scanning checkpoints for job %s: %v", ErrRecoveryFailed, jobID, err)
	}

	if latestCompleted != nil {
		state.latestCheckpoints[jobID] = latestCompleted
	}

	return nil
}
