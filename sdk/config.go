package sdk

import "time"

// ExecutionMode determines how the pipeline is executed.
type ExecutionMode uint8

const (
	// Embedded runs the pipeline in-process.
	Embedded ExecutionMode = iota
	// Cluster submits the pipeline to a remote cluster via RPC.
	Cluster
)

// RestartStrategy configures how the pipeline recovers from failures.
type RestartStrategy struct {
	Type              RestartStrategyType
	MaxAttempts       int
	Delay             time.Duration
	MaxDelay          time.Duration
	BackoffMultiplier float64
}

// RestartStrategyType identifies the restart policy.
type RestartStrategyType uint8

const (
	RestartFixedDelay         RestartStrategyType = iota // Restart with fixed delay between attempts.
	RestartExponentialBackoff                            // Restart with exponential backoff.
	RestartNone                                          // Do not restart on failure.
)

// FixedDelay returns a RestartStrategy that retries with a fixed delay.
func FixedDelay(maxAttempts int, delay time.Duration) RestartStrategy {
	return RestartStrategy{
		Type:        RestartFixedDelay,
		MaxAttempts: maxAttempts,
		Delay:       delay,
	}
}

// ExponentialBackoff returns a RestartStrategy with exponential backoff.
func ExponentialBackoff(maxAttempts int, initialDelay, maxDelay time.Duration, multiplier float64) RestartStrategy {
	return RestartStrategy{
		Type:              RestartExponentialBackoff,
		MaxAttempts:       maxAttempts,
		Delay:             initialDelay,
		MaxDelay:          maxDelay,
		BackoffMultiplier: multiplier,
	}
}

// NoRestart returns a RestartStrategy that does not restart on failure.
func NoRestart() RestartStrategy {
	return RestartStrategy{
		Type: RestartNone,
	}
}

// OutputTag identifies a named side output.
type OutputTag struct {
	Name string
}

// NewOutputTag creates a new OutputTag with the given name.
func NewOutputTag(name string) OutputTag {
	return OutputTag{Name: name}
}
