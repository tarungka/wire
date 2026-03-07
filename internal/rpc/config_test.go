package rpc

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		name     string
		got      any
		expected any
	}{
		{"SubmitJobTimeout", cfg.SubmitJobTimeout, 30 * time.Second},
		{"UpdateTaskStatusTimeout", cfg.UpdateTaskStatusTimeout, 5 * time.Second},
		{"TriggerCheckpointTimeout", cfg.TriggerCheckpointTimeout, 10 * time.Second},
		{"AcknowledgeCheckpointTimeout", cfg.AcknowledgeCheckpointTimeout, 5 * time.Second},
		{"RequestTaskSlotsTimeout", cfg.RequestTaskSlotsTimeout, 5 * time.Second},
		{"HeartbeatTimeout", cfg.HeartbeatTimeout, 2 * time.Second},
		{"MaxRetries", cfg.MaxRetries, 3},
		{"HeartbeatInterval", cfg.HeartbeatInterval, 5 * time.Second},
		{"SuspectThreshold", cfg.SuspectThreshold, 3},
		{"DeadThreshold", cfg.DeadThreshold, 5},
		{"MaxConsecutiveHeartbeatFailures", cfg.MaxConsecutiveHeartbeatFailures, 6},
		{"MaxConcurrentRPCs", cfg.MaxConcurrentRPCs, 256},
		{"MaxPayloadSize", cfg.MaxPayloadSize, 16 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}

func TestMethodTimeout(t *testing.T) {
	cfg := DefaultConfig()

	tests := []struct {
		method MethodID
		want   time.Duration
	}{
		{MethodSubmitJob, 30 * time.Second},
		{MethodUpdateTaskStatus, 5 * time.Second},
		{MethodTriggerCheckpoint, 10 * time.Second},
		{MethodAcknowledgeCheckpoint, 5 * time.Second},
		{MethodRequestTaskSlots, 5 * time.Second},
		{MethodHeartbeat, 2 * time.Second},
		{MethodID(0xFFFF), 30 * time.Second}, // unknown falls back to SubmitJob timeout
	}

	for _, tt := range tests {
		t.Run(MethodName(tt.method), func(t *testing.T) {
			got := cfg.methodTimeout(tt.method)
			if got != tt.want {
				t.Errorf("methodTimeout(%v) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}
