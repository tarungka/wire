package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

func TestStateBackendConfigurationPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wire.yaml")
	if err := os.WriteFile(path, []byte("state:\n  default_backend: hashmap\n  hashmap:\n    max_memory_mb: 8\n  pebble:\n    data_dir: /custom/state\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.DefaultBackend != "hashmap" || cfg.State.HashMap.MaxMemoryMB != 8 || cfg.State.Pebble.DataDir != "/custom/state" {
		t.Fatalf("file settings: %+v", cfg.State)
	}
	t.Setenv("WIRE_STATE_BACKEND", "pebble")
	t.Setenv("WIRE_STATE_HASHMAP_MAX_MEMORY_MB", "16")
	cfg, err = Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.DefaultBackend != "pebble" || cfg.State.HashMap.MaxMemoryMB != 16 {
		t.Fatalf("environment settings: %+v", cfg.State)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("state-backend", "pebble", "")
	flags.Int64("state-hashmap-max-memory-mb", 256, "")
	if err := ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	if cfg.State.HashMap.MaxMemoryMB != 16 {
		t.Fatal("flag default overrode environment")
	}
	if err := flags.Parse([]string{"--state-backend=hashmap", "--state-hashmap-max-memory-mb=0"}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	if cfg.State.DefaultBackend != "hashmap" || cfg.State.HashMap.MaxMemoryMB != 0 {
		t.Fatal("explicit flags not applied")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRE_STATE_DEFAULT_BACKEND", "hashmap")
	if _, err := Load([]string{path}); err == nil {
		t.Fatal("ambiguous backend environment aliases accepted")
	}
}

func TestStateBackendConfigurationValidation(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.State.DefaultBackend != "pebble" || cfg.State.HashMap.MaxMemoryMB != 256 || cfg.State.Pebble.DataDir != "/var/lib/wire/state" {
		t.Fatalf("defaults: %+v", cfg.State)
	}
	for _, value := range []int64{-1, math.MaxInt64/(1024*1024) + 1} {
		cfg := DefaultConfig()
		cfg.State.HashMap.MaxMemoryMB = value
		if cfg.Validate() == nil {
			t.Fatalf("accepted invalid memory limit %d", value)
		}
	}
	cfg.State.DefaultBackend = "unknown"
	if cfg.Validate() == nil {
		t.Fatal("accepted unknown backend")
	}
}
