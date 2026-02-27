package engine

import "time"

// Default configuration values per WIP-02 Section 3.1.
const (
	DefaultInputBufferSize     = 1024
	DefaultOutputBufferSize    = 1024
	DefaultAlignmentBufferSize = 4096
	DefaultDrainTimeout        = 5 * time.Second
	DefaultWatermarkInterval   = 200 * time.Millisecond
)

// TaskSlotConfig holds configuration for a single TaskSlot execution.
type TaskSlotConfig struct {
	InputBufferSize     int           // Per-input event channel capacity.
	OutputBufferSize    int           // Output channel capacity.
	AlignmentBufferSize int           // Per-input side buffer capacity for barrier alignment.
	DrainTimeout        time.Duration // Maximum time to drain channels on shutdown.
	WatermarkInterval   time.Duration // Watermark emission interval (source tasks only).
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
