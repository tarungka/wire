package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentSubstitutionHAAndReplicaFields(t *testing.T) {
	t.Setenv("WIRE_TEST_HOST", "localhost")
	t.Setenv("WIRE_TEST_ROOT", t.TempDir())
	path := filepath.Join(t.TempDir(), "node.yaml")
	data := `mode: worker
node:
  rpc_advertise_addr: '${WIRE_TEST_HOST}:4002'
worker:
  coordinator_seeds: ['${WIRE_TEST_HOST}:4002']
  epoch_path: '${WIRE_TEST_ROOT}/epoch'
  checkpoint_replica:
    listen_addr: '${WIRE_TEST_HOST}:4010'
    advertise_addr: '${WIRE_TEST_HOST}:4010'
    store_root: '${WIRE_TEST_ROOT}/store'
    artifact_root: '${WIRE_TEST_ROOT}/artifacts'
    staging_root: '${WIRE_TEST_ROOT}/staging'
election:
  kubernetes:
    api_server: 'https://${WIRE_TEST_HOST}'
    namespace: '${WIRE_TEST_NAMESPACE:-wire}'
    token_file: '${WIRE_TEST_ROOT}/token'
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.CoordinatorSeeds[0] != "localhost:4002" || cfg.Node.RPCAdvertiseAddr != "localhost:4002" || cfg.Worker.CheckpointReplica.AdvertiseAddr != "localhost:4010" || cfg.Election.Kubernetes.APIServer != "https://localhost" {
		t.Fatalf("unsubstituted configuration: %+v", cfg)
	}
	for _, value := range []string{cfg.Worker.EpochPath, cfg.Worker.CheckpointReplica.StoreRoot, cfg.Worker.CheckpointReplica.ArtifactRoot, cfg.Worker.CheckpointReplica.StagingRoot, cfg.Election.Kubernetes.TokenFile} {
		if !strings.HasPrefix(value, os.Getenv("WIRE_TEST_ROOT")+"/") {
			t.Fatalf("unsubstituted path %q", value)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentSubstitutionIdentifiesListElement(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Worker.CoordinatorSeeds = []string{"${WIRE_WIP13_UNSET_SEED}"}
	t.Setenv("WIRE_WIP13_UNSET_SEED", "")
	if err := os.Unsetenv("WIRE_WIP13_UNSET_SEED"); err != nil {
		t.Fatal(err)
	}
	err := envSubstConfig(&cfg)
	if err == nil || !strings.Contains(err.Error(), "worker.coordinator_seeds[0]") {
		t.Fatalf("error=%v", err)
	}
}
