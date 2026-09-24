package coordinator

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"

	"github.com/tarungka/wire/internal/protocol"
	"github.com/tarungka/wire/internal/rpc"
)

func TestBackendDefaultPersistsWithoutOverridingExplicitChoice(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.config.DefaultStateBackend = &rpc.StateBackendSpec{Type: "hashmap", MaxMemoryBytes: 8 * 1024 * 1024, DataDir: "unused-pebble-root"}
	graph := linearGraph()
	graph.Operators[1].Type = rpc.OperatorTypeProcess
	raw := encode(t, graph)
	original := bytes.Clone(raw)
	job, err := c.SubmitJob("default", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("submission mutated caller bytes")
	}
	persisted, err := store.Get(JobConfigKey(job.ID))
	if err != nil {
		t.Fatal(err)
	}
	var resolved rpc.JobGraph
	if err := protocol.DecodeMsgPack(persisted, &resolved); err != nil {
		t.Fatal(err)
	}
	spec := resolved.Operators[1].StateBackend
	if spec == nil || spec.Type != "hashmap" || spec.MaxMemoryBytes != 8*1024*1024 || spec.DataDir != "" {
		t.Fatalf("persisted backend: %+v", spec)
	}
	if resolved.Operators[0].StateBackend != nil || resolved.Operators[2].StateBackend != nil {
		t.Fatal("configured unmanaged operator")
	}
	recovered := New(CoordinatorConfig{DefaultStateBackend: &rpc.StateBackendSpec{Type: "pebble", DataDir: "replacement"}}, store, nil, zerolog.Nop())
	if err := recovered.recover(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recovered.jobs[job.ID].Config, persisted) {
		t.Fatal("recovery replaced the submitted backend")
	}
	resolvedAgain, err := recovered.resolveStateBackendDefaults(persisted)
	if err != nil || !bytes.Equal(resolvedAgain, persisted) {
		t.Fatal("resolved selection was not stable")
	}
	graph.Operators[1].StateBackend = &rpc.StateBackendSpec{Type: "pebble", DataDir: "explicit"}
	explicit := encode(t, graph)
	configured, err := c.resolveStateBackendDefaults(explicit)
	if err != nil || !bytes.Equal(configured, explicit) {
		t.Fatal("default replaced explicit SDK selection")
	}
}

func TestBackendDefaultsRejectInvalidBeforeJobReservation(t *testing.T) {
	c, store := newTestCoordinator(t)
	c.config.DefaultStateBackend = &rpc.StateBackendSpec{Type: "unknown"}
	graph := linearGraph()
	graph.Operators[1].Type = rpc.OperatorTypeProcess
	if _, err := c.SubmitJob("invalid", 1, encode(t, graph)); err == nil {
		t.Fatal("accepted invalid backend default")
	}
	if len(c.jobs) != 0 || len(c.activeJobNames) != 0 {
		t.Fatal("invalid default reserved job")
	}
	count := 0
	if err := store.PrefixScan([]byte(JobsPrefix), func(_, _ []byte) bool { count++; return true }); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid default persisted job")
	}
	opaque := []byte("legacy")
	result, err := c.resolveStateBackendDefaults(opaque)
	if err != nil || !bytes.Equal(result, opaque) {
		t.Fatal("changed opaque compatibility path")
	}
}
