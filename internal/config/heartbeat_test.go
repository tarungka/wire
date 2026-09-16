package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Heartbeat.Interval.Duration != 5*time.Second || cfg.Heartbeat.Timeout.Duration != 30*time.Second || cfg.Heartbeat.MaxFailures != 0 {
		t.Fatal("incorrect heartbeat defaults")
	}
	path := filepath.Join(t.TempDir(), "wire.yaml")
	if err := os.WriteFile(path, []byte("heartbeat:\n  interval: 2s\n  timeout: 12s\n  max_failures: 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Heartbeat.Interval.Duration != 2*time.Second || cfg.Heartbeat.Timeout.Duration != 12*time.Second || cfg.Heartbeat.MaxFailures != 4 {
		t.Fatal("heartbeat settings not loaded")
	}
	for _, tc := range []struct {
		name   string
		config HeartbeatConfig
	}{
		{"interval", HeartbeatConfig{Timeout: Duration{time.Second}}},
		{"timeout", HeartbeatConfig{Interval: Duration{time.Second}, Timeout: Duration{time.Second}}},
		{"max_failures", HeartbeatConfig{Interval: Duration{time.Second}, Timeout: Duration{2 * time.Second}, MaxFailures: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg.Heartbeat = tc.config
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "heartbeat."+tc.name) {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}
