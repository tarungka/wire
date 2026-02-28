package rpc

import "time"

// Default RPC configuration values per WIP-07 Section 2.2.
const (
	DefaultSubmitJobTimeout             = 30 * time.Second
	DefaultUpdateTaskStatusTimeout      = 5 * time.Second
	DefaultTriggerCheckpointTimeout     = 10 * time.Second
	DefaultAcknowledgeCheckpointTimeout = 5 * time.Second
	DefaultRequestTaskSlotsTimeout      = 5 * time.Second
	DefaultHeartbeatTimeout             = 2 * time.Second
	DefaultMaxRetries                   = 3
	DefaultHeartbeatInterval            = 5 * time.Second
	DefaultSuspectThreshold             = 3
	DefaultDeadThreshold                = 5
	MaxRPCPayloadSize                   = 16 * 1024 * 1024 // 16 MB
)

// Config holds RPC-level configuration.
type Config struct {
	SubmitJobTimeout             time.Duration
	UpdateTaskStatusTimeout      time.Duration
	TriggerCheckpointTimeout     time.Duration
	AcknowledgeCheckpointTimeout time.Duration
	RequestTaskSlotsTimeout      time.Duration
	HeartbeatTimeout             time.Duration
	MaxRetries                   int
	HeartbeatInterval            time.Duration
	SuspectThreshold             int
	DeadThreshold                int
	MaxPayloadSize               int
}

// DefaultConfig returns a Config populated with default values.
func DefaultConfig() Config {
	return Config{
		SubmitJobTimeout:             DefaultSubmitJobTimeout,
		UpdateTaskStatusTimeout:      DefaultUpdateTaskStatusTimeout,
		TriggerCheckpointTimeout:     DefaultTriggerCheckpointTimeout,
		AcknowledgeCheckpointTimeout: DefaultAcknowledgeCheckpointTimeout,
		RequestTaskSlotsTimeout:      DefaultRequestTaskSlotsTimeout,
		HeartbeatTimeout:             DefaultHeartbeatTimeout,
		MaxRetries:                   DefaultMaxRetries,
		HeartbeatInterval:            DefaultHeartbeatInterval,
		SuspectThreshold:             DefaultSuspectThreshold,
		DeadThreshold:                DefaultDeadThreshold,
		MaxPayloadSize:               MaxRPCPayloadSize,
	}
}

// methodTimeout returns the configured timeout for the given method ID.
func (c Config) methodTimeout(method MethodID) time.Duration {
	switch method {
	case MethodSubmitJob:
		return c.SubmitJobTimeout
	case MethodUpdateTaskStatus:
		return c.UpdateTaskStatusTimeout
	case MethodTriggerCheckpoint:
		return c.TriggerCheckpointTimeout
	case MethodAcknowledgeCheckpoint:
		return c.AcknowledgeCheckpointTimeout
	case MethodRequestTaskSlots:
		return c.RequestTaskSlotsTimeout
	case MethodHeartbeat:
		return c.HeartbeatTimeout
	default:
		return DefaultSubmitJobTimeout
	}
}
