package sdk

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/tarungka/wire/internal/engine"
)

func TestProcessAdapterRestoresRelocatedState(t *testing.T) {
	ctx := context.Background()
	originalRoot := t.TempDir()
	original := &processAdapter{config: NewPebbleStateBackend(originalRoot)}
	if err := original.Open(ctx); err != nil {
		t.Fatal(err)
	}
	if err := original.backend.Put([]byte("key"), []byte("state")); err != nil {
		t.Fatal(err)
	}
	handle, err := original.CheckpointState(7)
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := engine.ExportPebbleSnapshot(ctx, handle, &archive); err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(originalRoot); err != nil {
		t.Fatal(err)
	}
	relocated, err := engine.ImportPebbleSnapshot(ctx, &archive, t.TempDir(), 64*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	restored := &processAdapter{config: NewPebbleStateBackend(t.TempDir())}
	if err := restored.RestoreState(relocated); !errors.Is(err, engine.ErrBackendClosed) {
		t.Fatalf("restore before open: %v", err)
	}
	if err := restored.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.RestoreState(relocated); err != nil {
		t.Fatal(err)
	}
	value, err := restored.backend.Get([]byte("key"))
	if err != nil || string(value) != "state" {
		t.Fatalf("operator recovery: %q %v", value, err)
	}
}
