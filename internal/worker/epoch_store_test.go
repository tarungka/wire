package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerEpochSurvivesRestartAndRejectsRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "epoch")
	first, err := openEpochStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.close() }()
	if err := first.save(42); err != nil {
		t.Fatal(err)
	}
	if competing, err := openEpochStore(path); err == nil {
		_ = competing.close()
		t.Fatal("two workers own one epoch record")
	}
	if err := first.close(); err != nil {
		t.Fatal(err)
	}
	second, err := openEpochStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.close() }()
	if second.epoch != 42 {
		t.Fatalf("lost fence on restart: %d", second.epoch)
	}
	if err := second.save(41); err == nil {
		t.Fatal("accepted older leader after restart")
	}
	if err := second.save(43); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerEpochCorruptionFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "epoch")
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store, err := openEpochStore(path); err == nil {
		_ = store.close()
		t.Fatal("accepted corrupt epoch")
	}
	// A failed open releases ownership, without destroying the corrupt record.
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "bad" {
		t.Fatalf("overwrote epoch evidence: %q %v", data, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store, err := openEpochStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.close()
}
