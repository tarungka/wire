package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func inputFile(t *testing.T, n int) string {
	t.Helper()
	var data bytes.Buffer
	for i := range n {
		fmt.Fprintf(&data, "record-%d\n", i)
	}
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestFileSourceRestoreReplaysUncheckpointedRecords(t *testing.T) {
	path := inputFile(t, 70)
	first := &fileSource{path: path}
	if err := first.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	batch, err := first.ReadBatch(t.Context())
	if err != nil || len(batch) != 32 {
		t.Fatalf("batch=%d err=%v", len(batch), err)
	}
	batch[0].Value[0] = 'X'
	snapshot, err := first.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ReadBatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restored := &fileSource{path: path}
	if err := restored.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.RestoreOffset(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	count := 32
	for {
		batch, err := restored.ReadBatch(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if batch == nil {
			break
		}
		for _, e := range batch {
			if string(e.Value) != fmt.Sprint("record-", count) || string(e.Key) != fmt.Sprint(count) {
				t.Fatalf("record %d: %q %q", count, e.Key, e.Value)
			}
			count++
		}
	}
	if count != 70 {
		t.Fatalf("read %d records", count)
	}
}
func TestFileSourceRejectsIncompatibleRestore(t *testing.T) {
	path := inputFile(t, 2)
	s := &fileSource{path: path}
	if err := s.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, offset := range []fileOffset{{Version: 2, Digest: s.digest}, {Version: 1, Digest: "other"}, {Version: 1, Digest: s.digest, Next: -1}, {Version: 1, Digest: s.digest, Next: 3}} {
		data, _ := json.Marshal(offset)
		if err := s.RestoreOffset(t.Context(), data); err == nil {
			t.Fatalf("accepted %+v", offset)
		}
	}
	if err := s.RestoreOffset(t.Context(), []byte("invalid")); err == nil {
		t.Fatal("accepted malformed state")
	}
	snapshot, err := s.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	other := &fileSource{path: path}
	if err := other.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.RestoreOffset(t.Context(), snapshot); err == nil {
		t.Fatal("accepted changed replay file")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ReadBatch(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
func TestFileConnectorEmbeddedAndExport(t *testing.T) {
	path := inputFile(t, 3)
	var out bytes.Buffer
	if err := run(t.Context(), "embedded", path, "", "", "", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "record-0\nrecord-1\nrecord-2\n" {
		t.Fatalf("output=%q", out.String())
	}
	out.Reset()
	if err := run(t.Context(), "export", path, "", "http://localhost:4001", "", &out); err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Graph []byte `json:"graph_bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &exported); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(exported.Graph, []byte("immutable-lines")) {
		t.Fatalf("invalid export: %s", out.Bytes())
	}
}
