package coordinator

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLeadershipStoreRevocationAndDurableTakeover(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "metadata")
	open := func() (MetadataStore, error) { return NewPebbleStore(dir) }
	ctx, revoke := context.WithCancel(context.Background())
	first, err := OpenLeadershipStore(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { revoke(); _ = first.Close() })
	if err := first.WriteBatch([]KVPair{{Key: []byte("job"), Value: []byte("durable")}}); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenLeadershipStore(context.Background(), open); err == nil {
		_ = other.Close()
		t.Fatal("two terms opened the same authoritative database")
	}
	revoke()
	checks := []struct {
		name string
		call func() error
	}{
		{"get", func() error { _, err := first.Get([]byte("job")); return err }},
		{"set", func() error { return first.Set([]byte("job"), []byte("stale")) }},
		{"delete", func() error { return first.Delete([]byte("job")) }},
		{"batch", func() error { return first.WriteBatch([]KVPair{{Key: []byte("job"), Value: []byte("stale")}}) }},
		{"scan", func() error {
			return first.PrefixScan(nil, func([]byte, []byte) bool { t.Fatal("revoked scan callback"); return true })
		}},
		{"snapshot", func() error { return first.Snapshot(filepath.Join(t.TempDir(), "snapshot")) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, ErrNotLeader) {
				t.Fatalf("revoked access: %v", err)
			}
		})
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenLeadershipStore(context.Background(), open)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Set([]byte("job"), []byte("stale")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old handle revived: %v", err)
	}
	value, err := second.Get([]byte("job"))
	if err != nil || string(value) != "durable" {
		t.Fatalf("takeover lost durable metadata: %q %v", value, err)
	}
}

type blockedLeadershipStore struct {
	MetadataStore
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
}

func (s *blockedLeadershipStore) Set(key, value []byte) error {
	close(s.entered)
	<-s.release
	return s.MetadataStore.Set(key, value)
}
func (s *blockedLeadershipStore) Close() error { close(s.closed); return s.MetadataStore.Close() }

func TestLeadershipStoreCloseJoinsAdmittedWrite(t *testing.T) {
	backend := &blockedLeadershipStore{MetadataStore: NewMemoryStore(), entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	ctx, revoke := context.WithCancel(context.Background())
	defer revoke()
	store, err := OpenLeadershipStore(ctx, func() (MetadataStore, error) { return backend, nil })
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- store.Set([]byte("key"), []byte("value")) }()
	<-backend.entered
	revoke()
	closeDone := make(chan error, 1)
	go func() { closeDone <- store.Close() }()
	select {
	case <-backend.closed:
		t.Error("closed storage with an admitted write still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(backend.release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLeadershipStoreRevokedDuringOpen(t *testing.T) {
	ctx, revoke := context.WithCancel(context.Background())
	backend := &blockedLeadershipStore{MetadataStore: NewMemoryStore(), closed: make(chan struct{})}
	store, err := OpenLeadershipStore(ctx, func() (MetadataStore, error) { revoke(); return backend, nil })
	if store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("published canceled term: %v %v", store, err)
	}
	select {
	case <-backend.closed:
	default:
		t.Fatal("revoked open leaked storage lock")
	}
}
