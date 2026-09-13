package config

import (
	"strings"
	"testing"
)

func TestCheckpointReplicaRequiresStorageAndCapacity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Worker.CheckpointReplica.ListenAddr = "127.0.0.1:0"
	cfg.Worker.CheckpointReplica.Concurrency = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("invalid enabled replica accepted")
	}
	for _, field := range []string{"concurrency", "store_root", "artifact_root", "staging_root"} {
		if !strings.Contains(err.Error(), "worker.checkpoint_replica."+field) {
			t.Fatalf("missing error for %s: %v", field, err)
		}
	}
}
