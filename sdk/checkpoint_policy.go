package sdk

import (
	"time"

	"github.com/tarungka/wire/internal/rpc"
)

// SetCheckpointMinPause sets the quiet period after a completed checkpoint.
// Explicit savepoints and final checkpoints are exempt from this pacing.
func (env *StreamExecutionEnvironment) SetCheckpointMinPause(d time.Duration) *StreamExecutionEnvironment {
	env.checkpointMinPause = d
	return env
}

func (env *StreamExecutionEnvironment) checkpointPolicy() *rpc.CheckpointPolicy {
	return &rpc.CheckpointPolicy{Interval: env.checkpointInterval, Timeout: env.checkpointTimeout, MinPause: env.checkpointMinPause}
}
