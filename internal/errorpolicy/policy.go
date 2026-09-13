package errorpolicy

import (
	"fmt"
	"math"
	"time"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// Compile validates the policy before operator factories are invoked.
func Compile(p *rpc.ErrorPolicy, name string) (engine.ErrorHandlerConfig, error) {
	c := engine.ErrorHandlerConfig{OperatorName: name}
	if p == nil {
		return c, nil
	}
	if p.MaxRetries < 0 || p.MaxRetries > 10000 || p.InitialDelayMS < 0 || p.MaxDelayMS < 0 || p.InitialDelayMS > math.MaxInt64/int64(time.Millisecond) || p.MaxDelayMS > math.MaxInt64/int64(time.Millisecond) {
		return c, fmt.Errorf("invalid retry count or delay for operator %q", name)
	}
	c.MaxRetries = p.MaxRetries
	switch p.OnExhausted {
	case "", "fail":
		c.OnExhausted = engine.FailJob
	case "drop":
		c.OnExhausted = engine.DropEvent
	case "dlq":
		c.OnExhausted = engine.RouteToDLQ
	default:
		return c, fmt.Errorf("unknown exhausted action %q", p.OnExhausted)
	}
	switch p.Backoff {
	case "", "none":
	case "fixed":
		c.Backoff = engine.FixedBackoff(time.Duration(p.InitialDelayMS) * time.Millisecond)
	case "exponential":
		if p.InitialDelayMS <= 0 || p.MaxDelayMS < p.InitialDelayMS || math.IsNaN(p.Multiplier) || math.IsInf(p.Multiplier, 0) || p.Multiplier < 1 {
			return c, fmt.Errorf("invalid exponential backoff for operator %q", name)
		}
		c.Backoff = engine.ExponentialBackoff(time.Duration(p.InitialDelayMS)*time.Millisecond, time.Duration(p.MaxDelayMS)*time.Millisecond, p.Multiplier)
	default:
		return c, fmt.Errorf("unknown backoff %q", p.Backoff)
	}
	return c, nil
}
