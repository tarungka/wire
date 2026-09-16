package coordinator

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

// tornBatchFS stops inside the WAL write, after persisting only a prefix.
// The parent kills the process there, without allowing Commit or Close to run.
// This makes the crash position deterministic rather than relying on a sleep.
type tornBatchFS struct {
	vfs.FS
	armed  atomic.Bool
	marker string
}

func (f *tornBatchFS) Create(name string) (vfs.File, error) {
	file, err := f.FS.Create(name)
	if err != nil || !strings.HasSuffix(name, ".log") {
		return file, err
	}
	return &tornBatchFile{File: file, owner: f}, nil
}

type tornBatchFile struct {
	vfs.File
	owner *tornBatchFS
}

func (f *tornBatchFile) Write(data []byte) (int, error) {
	if !f.owner.armed.CompareAndSwap(true, false) {
		return f.File.Write(data)
	}
	n, err := f.File.Write(data[:len(data)/2])
	if err != nil {
		return n, err
	}
	if err := f.Sync(); err != nil {
		return n, err
	}
	if err := os.WriteFile(f.owner.marker, []byte("partial WAL persisted"), 0o600); err != nil {
		return n, err
	}
	select {} // Only the parent's process kill may end this interrupted write.
}

func TestPebbleBatchCrashHelper(t *testing.T) {
	root := os.Getenv("WIRE_BATCH_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	fs := &tornBatchFS{FS: vfs.Default, marker: filepath.Join(root, "interrupted")}
	db, err := pebble.Open(filepath.Join(root, "metadata"), &pebble.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production WriteBatch implementation with fault-injected IO.
	store := &PebbleStore{db: db}
	keys := []string{"jobs/test/meta", "jobs/test/graph", "jobs/test/assignments"}
	var initial, replacement []KVPair
	for _, key := range keys {
		initial = append(initial, KVPair{Key: []byte(key), Value: []byte("committed")})
		replacement = append(replacement, KVPair{Key: []byte(key), Value: bytes.Repeat([]byte("replacement"), 8192)})
	}
	if err := store.WriteBatch(initial); err != nil {
		t.Fatal(err)
	}
	fs.armed.Store(true)
	if err := store.WriteBatch(replacement); err != nil {
		t.Fatal(err)
	}
	t.Fatal("interrupted batch unexpectedly returned")
}

func TestPebbleBatchCrashIsAtomic(t *testing.T) {
	if testing.Short() {
		t.Skip("hard process-kill integration test")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^TestPebbleBatchCrashHelper$")
	cmd.Env = append(os.Environ(), "WIRE_BATCH_CRASH_ROOT="+root)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(root, "interrupted")); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("child exited before WAL interruption: %s", output.String())
		case <-deadline.C:
			t.Fatal("child never reached WAL interruption")
		case <-tick.C:
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-done
	store, err := NewPebbleStore(filepath.Join(root, "metadata"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for _, key := range []string{"jobs/test/meta", "jobs/test/graph", "jobs/test/assignments"} {
		value, err := store.Get([]byte(key))
		if err != nil || string(value) != "committed" {
			t.Fatalf("partial batch changed %s: value length=%d error=%v", key, len(value), err)
		}
	}
}
