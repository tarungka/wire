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

// Default checkpoint configuration values per WIP-05.
const (
	DefaultCheckpointTimeout                = 10 * time.Minute
	DefaultCheckpointMinPause               = 0
	DefaultMaxConsecutiveCheckpointFailures = 0   // 0 = unlimited
	DefaultTolerableCheckpointFailureRate   = 0.0 // 0 = no tolerance
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

// CheckpointConfig holds checkpoint timeout and failure tracking configuration.
type CheckpointConfig struct {
	Timeout                time.Duration // Max time to wait for barrier alignment. 0 → DefaultCheckpointTimeout.
	MinPause               time.Duration // Minimum pause between checkpoints. 0 → no pause.
	MaxConsecutiveFailures int           // Max consecutive failures before fatal error. 0 → unlimited.
	TolerableFailureRate   float64       // Max tolerable failure rate (0.0–1.0). 0 → no tolerance.
}

// WatermarkConfig holds watermark-specific configuration.
type WatermarkConfig struct {
	Strategy     WatermarkStrategyType // Watermark generation strategy.
	MaxOOO       time.Duration         // Max out-of-orderness (BoundedOOO only). 0 → DefaultMaxOOO (5s).
	EmitInterval time.Duration         // Watermark emission interval. 0 → DefaultWatermarkInterval (200ms).
	IdleTimeout  time.Duration         // Idle input detection timeout. 0 → DefaultIdleTimeout (1m).
}

// TaskSlotConfig holds configuration for a single TaskSlot execution.
type TaskSlotConfig struct {
	InputBufferSize     int              // Per-input event channel capacity.
	OutputBufferSize    int              // Output channel capacity.
	AlignmentBufferSize int              // Per-input side buffer capacity for barrier alignment.
	DrainTimeout        time.Duration    // Maximum time to drain channels on shutdown.
	WatermarkInterval   time.Duration    // Watermark emission interval (source tasks only). Deprecated: use Watermark.EmitInterval.
	Watermark           WatermarkConfig  // Watermark generation and propagation config.
	Checkpoint          CheckpointConfig // Checkpoint timeout and failure tracking config.
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
