package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

// generateSavepointID returns a unique savepoint identifier in the form "sp-<hex8>".
func generateSavepointID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return "sp-" + hex.EncodeToString(b)
}

// TriggerSavepoint creates a new in-progress savepoint for a running job.
func (c *Coordinator) TriggerSavepoint(jobID string) (*SavepointMeta, error) {
	if !c.IsReady() {
		return nil, ErrNotLeader
	}

	c.mu.RLock()
	job, ok := c.jobs[jobID]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrJobNotFound
	}

	if job.Status != JobRunning {
		return nil, ErrJobNotRunning
	}

	sp := &SavepointMeta{
		ID:          generateSavepointID(),
		JobID:       jobID,
		Status:      SavepointInProgress,
		TriggerTime: time.Now().UTC(),
	}

	if err := c.persistSavepoint(sp); err != nil {
		return nil, err
	}

	c.log.Info().Str("job_id", jobID).Str("savepoint_id", sp.ID).Msg("savepoint triggered")
	// TODO: inject checkpoint barriers via RPC
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
	if !c.IsReady() {
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

	if err := c.store.Delete(SavepointKey(jobID, spID)); err != nil {
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
