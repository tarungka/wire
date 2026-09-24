package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// SavepointCleanup retains exact replica identities until deletion receipts have
// been durably recorded. It must survive loss of the public savepoint entry.
type SavepointCleanup struct {
	Completed    map[string]bool   `codec:"completed,omitempty"`
	CompletedAt  time.Time         `codec:"completed_at,omitempty"`
	JobID        string            `codec:"job_id"`
	SavepointID  string            `codec:"savepoint_id"`
	CheckpointID uint64            `codec:"checkpoint_id"`
	EpochID      uint64            `codec:"epoch_id"`
	Replicas     map[string]string `codec:"replicas"`
	RequestedAt  time.Time         `codec:"requested_at"`
}

func savepointCleanupKey(jobID, savepointID string) []byte {
	return fmt.Appendf(nil, "jobs/%s/savepoint-cleanup/%s", jobID, savepointID)
}

func (c *Coordinator) persistSavepointDeletionLocked(sp SavepointMeta) error {
	batch := make([]KVPair, 0, 3)
	if sp.CheckpointID != 0 {
		raw, err := c.store.Get(CheckpointKey(sp.JobID, sp.CheckpointID))
		if err != nil {
			return err
		}
		if len(raw) != 0 {
			var cp CheckpointMeta
			if err := protocol.DecodeMsgPack(raw, &cp); err != nil {
				return err
			}
			if cp.JobID != sp.JobID || cp.ID != sp.CheckpointID || cp.EpochID != sp.EpochID || cp.SavepointID != sp.ID {
				return fmt.Errorf("savepoint checkpoint identity mismatch")
			}
			cleanup := SavepointCleanup{JobID: sp.JobID, SavepointID: sp.ID, CheckpointID: cp.ID, EpochID: cp.EpochID, Replicas: cp.Replicas, RequestedAt: time.Now().UTC()}
			cleanupRaw, err := protocol.EncodeMsgPack(cleanup)
			if err != nil {
				return err
			}
			// Keep outcome/manifests for inspection and transaction-commit fencing,
			// but disallow this archive as a recovery candidate before cleanup starts.
			if cp.InvalidReason == "" {
				cp.InvalidReason = "savepoint deleted"
			}
			cpRaw, err := protocol.EncodeMsgPack(cp)
			if err != nil {
				return err
			}
			batch = append(batch, KVPair{Key: savepointCleanupKey(sp.JobID, sp.ID), Value: cleanupRaw}, KVPair{Key: CheckpointKey(cp.JobID, cp.ID), Value: cpRaw})
		} else if sp.Status == SavepointCompleted {
			return fmt.Errorf("completed savepoint checkpoint missing")
		}
	}
	sp.Deleted = true
	sp.Queued = false
	// Deleted queued requests must never be reactivated by recovery.
	if sp.Status == SavepointInProgress {
		sp.Status = SavepointFailed
	}
	raw, err := protocol.EncodeMsgPack(sp)
	if err != nil {
		return err
	}
	batch = append(batch, KVPair{Key: SavepointKey(sp.JobID, sp.ID), Value: raw})
	return c.store.WriteBatch(batch)
}
