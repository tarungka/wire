package config

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Node defaults
	if cfg.Node.DataDir != "data/coordinator" {
		t.Errorf("Node.DataDir = %q, want %q", cfg.Node.DataDir, "data/coordinator")
	}
	if cfg.Node.StoreDB != "pebble" {
		t.Errorf("Node.StoreDB = %q, want %q", cfg.Node.StoreDB, "pebble")
	}
	if cfg.Node.Debug {
		t.Error("Node.Debug should default to false")
	}
	if cfg.Node.ID != "" {
		t.Errorf("Node.ID should default to empty, got %q", cfg.Node.ID)
	}

	// HTTP defaults
	if cfg.HTTP.Addr != ":4001" {
		t.Errorf("HTTP.Addr = %q, want %q", cfg.HTTP.Addr, ":4001")
	}

	// WriteQueue defaults
	if cfg.WriteQueue.Capacity != 1024 {
		t.Errorf("WriteQueue.Capacity = %d, want 1024", cfg.WriteQueue.Capacity)
	}
	if cfg.WriteQueue.BatchSize != 128 {
		t.Errorf("WriteQueue.BatchSize = %d, want 128", cfg.WriteQueue.BatchSize)
	}
	if cfg.WriteQueue.Timeout.Duration != 50*time.Millisecond {
		t.Errorf("WriteQueue.Timeout = %v, want 50ms", cfg.WriteQueue.Timeout.Duration)
	}

	// Election defaults
	if cfg.Election.Backend != "noop" {
		t.Errorf("Election.Backend = %q, want %q", cfg.Election.Backend, "noop")
	}
	if cfg.Election.LockPath != "data/coordinator/leader.lock" {
		t.Errorf("Election.LockPath = %q, want %q", cfg.Election.LockPath, "data/coordinator/leader.lock")
	}
}
