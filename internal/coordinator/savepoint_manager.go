package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// generateSavepointID returns a unique savepoint identifier in the form "sp-<hex32>".
func generateSavepointID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return "sp-" + hex.EncodeToString(b)
}

// TriggerSavepoint starts a savepoint or durably queues it behind the current
// checkpoint. Queued requests retain their IDs across coordinator recovery.
func (c *Coordinator) TriggerSavepoint(jobID string) (*SavepointMeta, error) {
	id := generateSavepointID()
	if _, err := c.triggerCheckpoint(jobID, id); err == nil {
		return c.GetSavepoint(jobID, id)
	} else if !errors.Is(err, ErrCheckpointInProgress) {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return nil, ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		return nil, ErrJobNotFound
	}
	if job.Status != JobRunning {
		return nil, ErrJobNotRunning
	}
	sp := &SavepointMeta{ID: id, JobID: jobID, Status: SavepointInProgress, Queued: true, TriggerTime: time.Now().UTC()}
	if err := c.persistSavepoint(sp); err != nil {
		return nil, err
	}
	c.queuedSavepointJobs[jobID] = true
	return sp, nil
}

// GetSavepoint retrieves a savepoint by job ID and savepoint ID.
func (c *Coordinator) GetSavepoint(jobID, spID string) (*SavepointMeta, error) {
	data, err := c.store.Get(SavepointKey(jobID, spID))
	if err != nil {
		return nil, fmt.Errorf("reading savepoint %s/%s: %w", jobID, spID, err)
	}
	if data == nil {
		return nil, ErrSavepointNotFound
	}

	var sp SavepointMeta
	if err := protocol.DecodeMsgPack(data, &sp); err != nil {
		return nil, fmt.Errorf("decoding savepoint %s/%s: %w", jobID, spID, err)
	}
	if sp.Deleted {
		return nil, ErrSavepointNotFound
	}
	return &sp, nil
}

// ListSavepoints returns all savepoints for a given job.
func (c *Coordinator) ListSavepoints(jobID string) ([]*SavepointMeta, error) {
	var result []*SavepointMeta
	var decodeErr error

	err := c.store.PrefixScan(SavepointsPrefix(jobID), func(key, value []byte) bool {
		var sp SavepointMeta
		if err := protocol.DecodeMsgPack(value, &sp); err != nil {
			decodeErr = fmt.Errorf("decoding savepoint %q: %w", string(key), err)
			return false
		}
		if sp.Deleted {
			return true
		}
		result = append(result, &sp)
		return true
	})
	if decodeErr != nil {
		return nil, decodeErr
	}
	if err != nil {
		return nil, fmt.Errorf("scanning savepoints for job %s: %w", jobID, err)
	}
	return result, nil
}

// DeleteSavepoint removes a savepoint from the store.
func (c *Coordinator) DeleteSavepoint(jobID, spID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.readyLocked() {
		return ErrNotLeader
	}

	// Verify savepoint exists.
	data, err := c.store.Get(SavepointKey(jobID, spID))
	if err != nil {
		return fmt.Errorf("reading savepoint %s/%s: %w", jobID, spID, err)
	}
	if data == nil {
		return ErrSavepointNotFound
	}

	var sp SavepointMeta
	if err := protocol.DecodeMsgPack(data, &sp); err != nil {
		return err
	}
	if sp.Deleted {
		return ErrSavepointNotFound
	}
	// A savepoint is also a normal recovery boundary. Physical cleanup must
	// not remove the latest selected boundary of an active job, even when it
	// was requested directly rather than by pause/rescale.
	if job := c.jobs[jobID]; job != nil && !job.Status.IsTerminal() && sp.CheckpointID != 0 && job.LatestCheckpoint == sp.CheckpointID {
		return ErrSavepointInUse
	}
	if job := c.jobs[jobID]; job != nil && !job.Status.IsTerminal() && (job.PauseSavepointID == sp.ID || (job.PauseCheckpoint != 0 && job.PauseCheckpoint == sp.CheckpointID)) {
		return ErrSavepointInUse
	}
	if job := c.jobs[jobID]; job != nil && !job.Status.IsTerminal() && job.RescaleCheckpoint != 0 && job.RescaleCheckpoint == sp.CheckpointID && job.LatestCheckpoint == sp.CheckpointID {
		return ErrSavepointInUse
	}
	for _, successor := range c.jobs {
		ref := successor.RestoreSavepoint
		if ref != nil && !successor.Status.IsTerminal() && ref.JobID == jobID && ref.SavepointID == spID {
			return ErrSavepointInUse
		}
	}
	if sp.Status == SavepointInProgress && !sp.Queued {
		return ErrCheckpointInProgress
	}

	if err := c.persistSavepointDeletionLocked(sp); err != nil {
		return fmt.Errorf("deleting savepoint %s/%s: %w", jobID, spID, err)
	}

	c.log.Info().Str("job_id", jobID).Str("savepoint_id", spID).Msg("savepoint deleted")
	return nil
}

// persistSavepoint encodes and stores a savepoint.
func (c *Coordinator) persistSavepoint(sp *SavepointMeta) error {
	data, err := protocol.EncodeMsgPack(sp)
	if err != nil {
		return fmt.Errorf("encoding savepoint %s: %w", sp.ID, err)
	}
	if err := c.store.Set(SavepointKey(sp.JobID, sp.ID), data); err != nil {
		return fmt.Errorf("persisting savepoint %s: %w", sp.ID, err)
	}
	return nil
}
