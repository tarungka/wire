package coordinator

import (
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// savepointDecisionLocked returns a write to commit with the checkpoint
// decision. No separately visible completed savepoint can precede durability.
func (c *Coordinator) savepointDecisionLocked(cp CheckpointMeta) (*KVPair, error) {
	if cp.SavepointID == "" {
		return nil, nil
	}
	sp, err := c.GetSavepoint(cp.JobID, cp.SavepointID)
	if err != nil {
		return nil, err
	}
	if sp.CheckpointID != cp.ID || sp.EpochID != cp.EpochID {
		return nil, fmt.Errorf("savepoint checkpoint identity mismatch")
	}
	switch cp.Status {
	case CheckpointCompleted:
		sp.Status = SavepointCompleted
		sp.Path = fmt.Sprintf("jobs/%s/checkpoints/%d", cp.JobID, cp.ID)
	case CheckpointAborted:
		sp.Status = SavepointFailed
	default:
		return nil, fmt.Errorf("savepoint requires terminal checkpoint decision")
	}
	if sp.CompletionTime.IsZero() {
		sp.CompletionTime = time.Now().UTC()
	}
	data, err := protocol.EncodeMsgPack(sp)
	if err != nil {
		return nil, err
	}
	return &KVPair{Key: SavepointKey(cp.JobID, cp.SavepointID), Value: data}, nil
}
