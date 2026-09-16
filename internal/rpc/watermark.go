package rpc

import (
	"fmt"
	"time"
)

// WatermarkConfig is source configuration carried with the operator graph.
// Durations use nanoseconds in msgpack, avoiding lossy millisecond conversion.
type WatermarkConfig struct {
	Strategy     string         `codec:"strategy" yaml:"strategy"`
	MaxOOO       *time.Duration `codec:"max_ooo,omitempty" yaml:"max_ooo"`
	EmitInterval time.Duration  `codec:"emit_interval,omitempty" yaml:"emit_interval"`
	IdleTimeout  time.Duration  `codec:"idle_timeout,omitempty" yaml:"idle_timeout"`
}

func (c WatermarkConfig) Validate() error {
	switch c.Strategy {
	case "bounded-ooo", "monotonic", "ingestion-time":
	default:
		return fmt.Errorf("unknown watermark strategy %q", c.Strategy)
	}
	if (c.MaxOOO != nil && *c.MaxOOO < 0) || c.EmitInterval < 0 || c.IdleTimeout < 0 {
		return fmt.Errorf("watermark durations cannot be negative")
	}
	if c.Strategy != "bounded-ooo" && c.MaxOOO != nil {
		return fmt.Errorf("max_ooo is only valid for bounded-ooo watermarks")
	}
	return nil
}
