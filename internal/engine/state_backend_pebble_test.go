package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func pebbleBackendForTest(t *testing.T, dir string) StateBackend {
	t.Helper()
	b, err := NewStateBackend(StateBackendConfig{Type: StateBackendPebble, PebbleDataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestPebbleStateBackend_Contract(t *testing.T) {
	runStateBackendContractTests(t, "Pebble", func(t *testing.T) StateBackend { return pebbleBackendForTest(t, t.TempDir()) })
}
func TestPebbleStateBackend_RestoreReopenAndCorruption(t *testing.T) {
	dir := t.TempDir()
	b := pebbleBackendForTest(t, dir)
	if err := b.Put([]byte("key"), []byte("snapshot")); err != nil {
		t.Fatal(err)
	}
	handle, err := b.Checkpoint(9)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	b = pebbleBackendForTest(t, dir)
	if err = b.Put([]byte("key"), []byte("later")); err != nil {
		t.Fatal(err)
	}
	if err = b.Restore(handle); err != nil {
		t.Fatal(err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	b = pebbleBackendForTest(t, dir)
	defer func() { _ = b.Close() }()
	value, err := b.Get([]byte("key"))
	if err != nil || string(value) != "snapshot" {
		t.Fatalf("restored reopen: %q %v", value, err)
	}
	var manifest pebbleSnapshotManifest
	if err = json.Unmarshal(handle.Data, &manifest); err != nil {
		t.Fatal(err)
	}
	// Corrupt a private copy, never an SST hard-linked to a running DB.
	copyDir := t.TempDir()
	for name := range manifest.Files {
		if err = copyStateFile(filepath.Join(manifest.Path, name), filepath.Join(copyDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	manifest.Path = copyDir
	for name := range manifest.Files {
		if err = os.WriteFile(filepath.Join(copyDir, name), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		break
	}
	handle.Data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Restore(handle); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("corrupt restore: %v", err)
	}
	value, err = b.Get([]byte("key"))
	if err != nil || string(value) != "snapshot" {
		t.Fatalf("failed restore changed state: %q %v", value, err)
	}
}
func TestPebbleStateBackend_IteratorAndLock(t *testing.T) {
	dir := t.TempDir()
	b := pebbleBackendForTest(t, dir)
	if other, err := NewStateBackend(StateBackendConfig{PebbleDataDir: dir}); err == nil {
		_ = other.Close()
		t.Fatal("second backend acquired same state directory")
	}
	for _, key := range [][]byte{{0xff}, {0xff, 0xff}, {0xfe}} {
		if err := b.Put(key, key); err != nil {
			t.Fatal(err)
		}
	}
	it := b.NewIterator([]byte{0xff})
	count := 0
	for it.Next() {
		count++
	}
	if count != 2 {
		t.Fatalf("all-FF prefix returned %d keys", count)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if it.Next() {
		t.Fatal("iterator remained valid after close")
	}
	it.Close()
	empty := b.NewIterator(nil)
	if empty.Next() {
		t.Fatal("closed backend returned entries")
	}
	empty.Close()
}

func TestPebbleStateBackend_MissingGenerationFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ACTIVE"), []byte("generation-missing"), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := NewStateBackend(StateBackendConfig{PebbleDataDir: dir}); err == nil {
		_ = b.Close()
		t.Fatal("missing durable generation silently opened empty state")
	}
}

func TestPebbleStateBackend_RestoreAnotherDirectory(t *testing.T) {
	source := pebbleBackendForTest(t, t.TempDir())
	if err := source.Put([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	handle, err := source.Checkpoint(5)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	dest := pebbleBackendForTest(t, t.TempDir())
	defer func() { _ = dest.Close() }()
	if err = dest.Put([]byte("extra"), nil); err != nil {
		t.Fatal(err)
	}
	it := dest.NewIterator(nil)
	if err = dest.Restore(handle); err != nil {
		t.Fatal(err)
	}
	if it.Next() {
		t.Fatal("restore left iterator on obsolete state")
	}
	it.Close()
	value, err := dest.Get([]byte("key"))
	if err != nil || string(value) != "value" {
		t.Fatalf("cross-directory restore: %q %v", value, err)
	}
	if _, err = dest.Get([]byte("extra")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("restore retained extra key: %v", err)
	}
	handle.CheckpointID++
	if err = dest.Restore(handle); !errors.Is(err, ErrSnapshotCorrupt) {
		t.Fatalf("checkpoint identity mismatch: %v", err)
	}
}
