package rpc

import (
	"fmt"
	"math"
	"time"
)

// RestartPolicy is a job's explicit recovery budget. MaxAttempts counts
// redeployments after failure, not the initial deployment or requested rescales.
// Nil selects the coordinator's default policy, including its stable-run reset.
type RestartPolicy struct {
	Type        RestartStrategyType `codec:"type"`
	MaxAttempts int                 `codec:"max_attempts"`
	Delay       time.Duration       `codec:"delay"`
	MaxDelay    time.Duration       `codec:"max_delay"`
	Multiplier  float64             `codec:"multiplier"`
}

func (p *RestartPolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.MaxAttempts < 0 || p.Delay < 0 || p.MaxDelay < 0 || math.IsNaN(p.Multiplier) || math.IsInf(p.Multiplier, 0) {
		return fmt.Errorf("restart attempts and delays must be nonnegative and multiplier finite")
	}
	switch p.Type {
	case RestartStrategyNoRestart:
		if p.MaxAttempts != 0 {
			return fmt.Errorf("no-restart policy must have zero attempts")
		}
	case RestartStrategyFixedDelay:
	case RestartStrategyExponentialBackoff:
		if p.Multiplier < 1 || p.MaxDelay < p.Delay {
			return fmt.Errorf("exponential restart requires multiplier >= 1 and max delay >= initial delay")
		}
	default:
		return fmt.Errorf("unknown restart policy %d", p.Type)
	}
	return nil
}

// DelayAfter returns the delay before the next attempt, including the first
// retry. Saturation happens before conversion to Duration to avoid overflow.
func (p *RestartPolicy) DelayAfter(completedAttempts int) time.Duration {
	if p.Type == RestartStrategyNoRestart {
		return 0
	}
	if p.Type != RestartStrategyExponentialBackoff || p.Delay == 0 {
		return p.Delay
	}
	delay := float64(p.Delay) * math.Pow(p.Multiplier, float64(max(0, completedAttempts)))
	if delay >= float64(p.MaxDelay) {
		return p.MaxDelay
	}
	return time.Duration(delay)
}
