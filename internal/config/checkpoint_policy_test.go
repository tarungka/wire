package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckpointPolicyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.yaml")
	if err := os.WriteFile(path, []byte("checkpoint:\n  timeout: 20s\n  min_pause: 3s\n  max_consecutive_failures: 3\n  tolerable_failure_rate: 0.5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checkpoint.Timeout.Duration != 20*time.Second || cfg.Checkpoint.MinPause.Duration != 3*time.Second || cfg.Checkpoint.MaxConsecutiveFailures != 3 || cfg.Checkpoint.TolerableFailureRate != 0.5 {
		t.Fatalf("policy not loaded: %+v", cfg.Checkpoint)
	}
	for _, rate := range []float64{-1, 1.1, math.NaN(), math.Inf(1)} {
		cfg := DefaultConfig()
		cfg.Checkpoint.TolerableFailureRate = rate
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tolerable_failure_rate") {
			t.Fatalf("rate %v: %v", rate, err)
		}
	}
	cfg.Checkpoint.MinPause.Duration = -time.Second
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "min_pause") {
		t.Fatalf("negative min pause: %v", err)
	}
}
