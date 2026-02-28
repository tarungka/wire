package engine

import "time"

// Default configuration values per WIP-02 Section 3.1.
const (
	DefaultInputBufferSize     = 1024
	DefaultOutputBufferSize    = 1024
	DefaultAlignmentBufferSize = 4096
	DefaultDrainTimeout        = 5 * time.Second
	DefaultWatermarkInterval   = 200 * time.Millisecond
	DefaultIdleTimeout         = 1 * time.Minute
	DefaultMaxOOO              = 5 * time.Second
)

// WatermarkStrategyType identifies a watermark generation strategy.
type WatermarkStrategyType uint8

const (
	// StrategyNone means no explicit strategy — use legacy source watermark.
	StrategyNone WatermarkStrategyType = iota
	// StrategyBoundedOOO allows events to arrive out of order up to MaxOOO.
	StrategyBoundedOOO
	// StrategyMonotonic assumes events arrive in order (maxOOO=0).
	StrategyMonotonic
	// StrategyIngestionTime uses the current wall clock as the watermark.
	StrategyIngestionTime
)

// WatermarkConfig holds watermark-specific configuration.
type WatermarkConfig struct {
	Strategy     WatermarkStrategyType // Watermark generation strategy.
	MaxOOO       time.Duration         // Max out-of-orderness (BoundedOOO only). Default: 5s.
	EmitInterval time.Duration         // Watermark emission interval. Default: 200ms.
	IdleTimeout  time.Duration         // Idle source detection timeout. Default: 1m.
}

// TaskSlotConfig holds configuration for a single TaskSlot execution.
type TaskSlotConfig struct {
	InputBufferSize     int             // Per-input event channel capacity.
	OutputBufferSize    int             // Output channel capacity.
	AlignmentBufferSize int             // Per-input side buffer capacity for barrier alignment.
	DrainTimeout        time.Duration   // Maximum time to drain channels on shutdown.
	WatermarkInterval   time.Duration   // Watermark emission interval (source tasks only). Deprecated: use Watermark.EmitInterval.
	Watermark           WatermarkConfig // Watermark generation and propagation config.
}

// DefaultTaskSlotConfig returns a TaskSlotConfig populated with default values.
func DefaultTaskSlotConfig() TaskSlotConfig {
	return TaskSlotConfig{
		InputBufferSize:     DefaultInputBufferSize,
		OutputBufferSize:    DefaultOutputBufferSize,
		AlignmentBufferSize: DefaultAlignmentBufferSize,
		DrainTimeout:        DefaultDrainTimeout,
		WatermarkInterval:   DefaultWatermarkInterval,
	}
}
