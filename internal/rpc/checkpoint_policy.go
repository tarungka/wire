package rpc

import (
	"fmt"
	"time"
)

// CheckpointPolicy is persisted with a job. Nil selects coordinator defaults;
// a zero interval disables automatic checkpoints, but allows manual savepoints.
// Wire currently permits one checkpoint in flight per job.
type CheckpointPolicy struct {
	Interval time.Duration `codec:"interval"`
	Timeout  time.Duration `codec:"timeout"`
	MinPause time.Duration `codec:"min_pause"`
}

func (p *CheckpointPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.Interval < 0 || p.MinPause < 0 || p.Timeout <= 0 {
		return fmt.Errorf("checkpoint interval and minimum pause must be nonnegative, and timeout must be positive")
	}
	return nil
}
