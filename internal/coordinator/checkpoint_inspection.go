package coordinator

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tarungka/wire/internal/protocol"
)

type jobCheckpointResponse struct {
	LatestCompleted  uint64 `json:"latest_completed"`
	LatestDurationMs *int64 `json:"latest_duration_ms,omitempty"`
	TotalCompleted   uint64 `json:"total_completed"`
	TotalFailed      uint64 `json:"total_failed"`
	InProgress       uint64 `json:"in_progress"`
}

// Counts include savepoints and final boundaries. These describe persisted
// outcomes, not the checkpoint failure-policy budget (which exempts savepoints).
func (c *Coordinator) checkpointInspection(jobID string) (jobCheckpointResponse, error) {
	var result jobCheckpointResponse
	var decodeErr error
	prefix := []byte(fmt.Sprintf("jobs/%s/checkpoints/", jobID))
	err := c.store.PrefixScan(prefix, func(key, value []byte) bool {
		suffix := strings.TrimPrefix(string(key), string(prefix))
		if suffix == "latest" || strings.Contains(suffix, "/") {
			return true
		}
		var cp CheckpointMeta
		decodeErr = protocol.DecodeMsgPack(value, &cp)
		if decodeErr != nil {
			return false
		}
		if cp.JobID != jobID || !bytes.Equal(key, CheckpointKey(jobID, cp.ID)) {
			decodeErr = fmt.Errorf("checkpoint identity mismatch")
			return false
		}
		switch cp.Status {
		case CheckpointCompleted:
			result.TotalCompleted++
			if cp.ID > result.LatestCompleted {
				result.LatestCompleted = cp.ID
				result.LatestDurationMs = nil
				if !cp.CompletedAt.IsZero() && !cp.Timestamp.IsZero() && !cp.CompletedAt.Before(cp.Timestamp) {
					duration := cp.CompletedAt.Sub(cp.Timestamp).Milliseconds()
					result.LatestDurationMs = &duration
				}
			}
		case CheckpointAborted, CheckpointFailed:
			result.TotalFailed++
		case CheckpointTriggered, CheckpointInProgress:
			result.InProgress++
		default:
			decodeErr = fmt.Errorf("unknown checkpoint status")
			return false
		}
		return true
	})
	if err != nil {
		return jobCheckpointResponse{}, err
	}
	return result, decodeErr
}
