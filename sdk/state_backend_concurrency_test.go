package sdk

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateBackendCompactionConfiguration(t *testing.T) {
	cfg := NewPebbleStateBackend(t.TempDir())
	cfg.MaxCompactionConcurrency = -1
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative limit: %v", err)
	}
	cfg.MaxCompactionConcurrency = 3
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	backend, cleanup, err := cfg.open(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer func() { _ = backend.Close() }()
	options, err := filepath.Glob(filepath.Join(cfg.DataDir, "operator-1", "instance-2", "generation-*", "OPTIONS-*"))
	if err != nil || len(options) != 1 {
		t.Fatalf("options: %v %v", options, err)
	}
	data, err := os.ReadFile(options[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "max_concurrent_compactions=3\n") {
		t.Fatal("SDK did not pass compaction limit to Pebble")
	}
}
