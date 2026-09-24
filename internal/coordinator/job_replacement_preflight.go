package coordinator

import "fmt"

// ValidateReplacementLayout checks the physical state layout before stopping a
// running predecessor. It makes no changes and does not reserve the job against
// concurrent changes. Actual submission must still validate its saved boundary,
// archive integrity, ownership and application state when restoring.
func (c *Coordinator) ValidateReplacementLayout(jobID string, parallelism int, config []byte) error {
	if parallelism < 1 {
		return fmt.Errorf("%w: parallelism must be positive", ErrInvalidConfig)
	}
	c.mu.RLock()
	if !c.readyLocked() {
		c.mu.RUnlock()
		return ErrNotLeader
	}
	job := c.jobs[jobID]
	if job == nil {
		c.mu.RUnlock()
		return ErrJobNotFound
	}
	if job.Status != JobRunning {
		c.mu.RUnlock()
		return ErrJobNotRunning
	}
	old := *job
	old.Config = append([]byte(nil), job.Config...)
	c.mu.RUnlock()
	resolved, err := c.resolveStateBackendDefaults(config)
	if err != nil {
		return err
	}
	next := JobMeta{ID: old.ID + "-replacement", Parallelism: parallelism, Config: resolved}
	sources, err := generateTaskDescriptors(&old)
	if err != nil {
		return err
	}
	targets, err := generateTaskDescriptors(&next)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("%w: empty source layout", ErrInvalidConfig)
	}
	_, err = planTaskLayoutRestoreMode(sources[0].NumKeyGroups, sources, targets, true)
	return err
}
