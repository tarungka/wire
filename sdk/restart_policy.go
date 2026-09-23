package sdk

import (
	"fmt"

	"github.com/tarungka/wire/internal/rpc"
)

func (env *StreamExecutionEnvironment) restartPolicy() (*rpc.RestartPolicy, error) {
	s := env.restartStrategy
	p := &rpc.RestartPolicy{MaxAttempts: s.MaxAttempts, Delay: s.Delay, MaxDelay: s.MaxDelay, Multiplier: s.BackoffMultiplier}
	switch s.Type {
	case RestartNone:
		p.Type = rpc.RestartStrategyNoRestart
	case RestartFixedDelay:
		p.Type = rpc.RestartStrategyFixedDelay
	case RestartExponentialBackoff:
		p.Type = rpc.RestartStrategyExponentialBackoff
	default:
		return nil, fmt.Errorf("%w: unknown restart strategy %d", ErrInvalidConfig, s.Type)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return p, nil
}
