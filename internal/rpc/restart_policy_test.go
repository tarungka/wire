package rpc

import (
	"math"
	"testing"
	"time"
)

func TestRestartPolicyDelayAndValidation(t *testing.T) {
	p := &RestartPolicy{Type: RestartStrategyExponentialBackoff, MaxAttempts: 5, Delay: time.Second, MaxDelay: 5 * time.Second, Multiplier: 2}
	for attempts, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		if got := p.DelayAfter(attempts); got != want {
			t.Fatalf("attempt %d: %v != %v", attempts, got, want)
		}
	}
	if got := p.DelayAfter(math.MaxInt); got != p.MaxDelay {
		t.Fatalf("delay overflowed: %v", got)
	}
	p.Type = RestartStrategyFixedDelay
	if got := p.DelayAfter(math.MaxInt); got != time.Second {
		t.Fatalf("fixed delay grew: %v", got)
	}
	for _, bad := range []RestartPolicy{
		{Type: RestartStrategyUnknown},
		{Type: RestartStrategyNoRestart, MaxAttempts: 1},
		{Type: RestartStrategyFixedDelay, MaxAttempts: -1},
		{Type: RestartStrategyFixedDelay, Delay: -1},
		{Type: RestartStrategyExponentialBackoff, Multiplier: math.NaN()},
		{Type: RestartStrategyExponentialBackoff, Multiplier: math.Inf(1)},
		{Type: RestartStrategyExponentialBackoff, Multiplier: .5},
		{Type: RestartStrategyExponentialBackoff, Delay: time.Second, Multiplier: 2},
	} {
		if bad.Validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", bad)
		}
	}
}
