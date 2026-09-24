package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScopedStateBackendSeparatesJobsOperatorsAndInstances(t *testing.T) {
	root := t.TempDir()
	config := StateBackendConfig{Type: StateBackendPebble, PebbleDataDir: root, PebbleMaxCompactionConcurrency: 1}
	for _, identity := range []struct {
		job, operator string
		instance      int
	}{{"job", "operator", 0}, {"other-job", "operator", 0}, {"job", "other-operator", 0}, {"job", "operator", 1}, {"../../outside", "operator", 0}} {
		backend, cleanup, err := ScopedStateBackendFactory(config, identity.job, identity.operator, "attempt", identity.instance)()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Get([]byte("key")); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("state leaked across identity: %v", err)
		}
		if err := backend.Put([]byte("key"), []byte("value")); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		cleanup()
	}
	backend, cleanup, err := ScopedStateBackendFactory(config, "job", "operator", "attempt", 0)()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer backend.Close()
	if got, err := backend.Get([]byte("key")); err != nil || string(got) != "value" {
		t.Fatalf("explicit state not retained: %q %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("job/operator directories: %d", len(entries))
	}
	for _, entry := range entries {
		if len(entry.Name()) != 64 || !entry.IsDir() {
			t.Fatal("identity was not safely encoded")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
		t.Fatal("raw job identity used as path")
	}
}

func TestScopedStateBackendReplacementAttemptStartsEmpty(t *testing.T) {
	config := StateBackendConfig{Type: StateBackendPebble, PebbleDataDir: t.TempDir()}
	for _, attempt := range []string{"first", "replacement"} {
		backend, cleanup, err := ScopedStateBackendFactory(config, "job", "operator", attempt, 0)()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Get([]byte("uncheckpointed")); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("replacement inherited uncheckpointed state: %v", err)
		}
		if err := backend.Put([]byte("uncheckpointed"), []byte("mutation")); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		cleanup()
	}
}
