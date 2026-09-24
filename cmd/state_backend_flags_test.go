package main

import (
	"os"
	"testing"

	"github.com/tarungka/wire/internal/config"
)

func TestStateBackendFlagsReachNodeConfiguration(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"wire", "--state-backend=hashmap", "--state-hashmap-max-memory-mb=12"}
	_, flags, err := initFlags("wire", "test", &BuildInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.ApplyFlags(&cfg, flags); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.State.DefaultBackend != "hashmap" || cfg.State.HashMap.MaxMemoryMB != 12 {
		t.Fatalf("backend flags lost: %+v", cfg.State)
	}
}
