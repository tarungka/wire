package coordinator

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestPebbleStore(t *testing.T) *PebbleStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPebbleStore_GetSetDelete(t *testing.T) {
	s := newTestPebbleStore(t)

	// Get non-existent key returns nil.
	val, err := s.Get([]byte("missing"))
	if err != nil {
		t.Fatalf("Get missing: %v", err)
	}
	if val != nil {
		t.Fatalf("expected nil, got %v", val)
	}

	// Set then Get.
	if err := s.Set([]byte("key1"), []byte("value1")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, err = s.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "value1" {
		t.Fatalf("expected value1, got %s", val)
	}

	// Overwrite.
	if err := s.Set([]byte("key1"), []byte("value2")); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	val, err = s.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get overwrite: %v", err)
	}
	if string(val) != "value2" {
		t.Fatalf("expected value2, got %s", val)
	}

	// Delete.
	if err := s.Delete([]byte("key1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	val, err = s.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if val != nil {
		t.Fatalf("expected nil after delete, got %v", val)
	}

	// Delete non-existent key is a no-op.
	if err := s.Delete([]byte("missing")); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestPebbleStore_WriteBatch(t *testing.T) {
	s := newTestPebbleStore(t)

	batch := []KVPair{
		{Key: []byte("b"), Value: []byte("2")},
		{Key: []byte("a"), Value: []byte("1")},
		{Key: []byte("c"), Value: []byte("3")},
	}
	if err := s.WriteBatch(batch); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	for _, kv := range batch {
		val, err := s.Get(kv.Key)
		if err != nil {
			t.Fatalf("Get %s: %v", kv.Key, err)
		}
		if string(val) != string(kv.Value) {
			t.Fatalf("key %s: expected %s, got %s", kv.Key, kv.Value, val)
		}
	}
}

func TestPebbleStore_PrefixScan(t *testing.T) {
	s := newTestPebbleStore(t)

	entries := []KVPair{
		{Key: []byte("jobs/a/meta"), Value: []byte("1")},
		{Key: []byte("jobs/b/meta"), Value: []byte("2")},
		{Key: []byte("jobs/c/meta"), Value: []byte("3")},
		{Key: []byte("workers/w1/meta"), Value: []byte("4")},
	}
	if err := s.WriteBatch(entries); err != nil {
		t.Fatal(err)
	}

	var keys []string
	err := s.PrefixScan([]byte("jobs/"), func(key, value []byte) bool {
		keys = append(keys, string(key))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(keys), keys)
	}
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Fatalf("keys not in sorted order: %v", keys)
		}
	}
}

func TestPebbleStore_PrefixScan_MixedPrefixes(t *testing.T) {
	s := newTestPebbleStore(t)

	entries := []KVPair{
		{Key: []byte("cluster/config"), Value: []byte("cfg")},
		{Key: []byte("cluster/epoch"), Value: []byte("1")},
		{Key: []byte("cluster/leader"), Value: []byte("n1")},
		{Key: []byte("jobs/j1/meta"), Value: []byte("j1")},
		{Key: []byte("workers/w1/meta"), Value: []byte("w1")},
	}
	if err := s.WriteBatch(entries); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		prefix   string
		expected int
	}{
		{"cluster/", 3},
		{"jobs/", 1},
		{"workers/", 1},
		{"missing/", 0},
	}
	for _, tt := range tests {
		count := 0
		if err := s.PrefixScan([]byte(tt.prefix), func(key, value []byte) bool {
			count++
			return true
		}); err != nil {
			t.Fatalf("PrefixScan %q: %v", tt.prefix, err)
		}
		if count != tt.expected {
			t.Errorf("prefix %q: expected %d, got %d", tt.prefix, tt.expected, count)
		}
	}
}

func TestPebbleStore_PrefixScan_EarlyStop(t *testing.T) {
	s := newTestPebbleStore(t)

	for i := range 10 {
		if err := s.Set(fmt.Appendf(nil, "prefix/%02d", i), []byte("x")); err != nil {
			t.Fatalf("Set prefix/%02d: %v", i, err)
		}
	}

	count := 0
	if err := s.PrefixScan([]byte("prefix/"), func(key, value []byte) bool {
		count++
		return count < 3
	}); err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 iterations, got %d", count)
	}
}

func TestPebbleStore_WALRecovery(t *testing.T) {
	dir := t.TempDir()

	// Write data and close.
	s1, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s1.Set([]byte("persist-key"), []byte("persist-val")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify data survived.
	s2, err := NewPebbleStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()

	val, err := s2.Get([]byte("persist-key"))
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if string(val) != "persist-val" {
		t.Fatalf("expected persist-val, got %s", val)
	}
}

func TestPebbleStore_Snapshot(t *testing.T) {
	s := newTestPebbleStore(t)

	if err := s.Set([]byte("snap-key"), []byte("snap-val")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	snapDir := filepath.Join(t.TempDir(), "snapshot")
	if err := s.Snapshot(snapDir); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Verify the snapshot directory exists and is non-empty.
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		t.Fatalf("ReadDir snapshot: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("snapshot directory is empty")
	}

	// Open the snapshot as a new PebbleDB and verify data.
	s2, err := NewPebbleStore(snapDir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = s2.Close() }()

	val, err := s2.Get([]byte("snap-key"))
	if err != nil {
		t.Fatalf("Get from snapshot: %v", err)
	}
	if string(val) != "snap-val" {
		t.Fatalf("expected snap-val, got %s", val)
	}
}

func TestPebbleStore_ConcurrentAccess(t *testing.T) {
	s := newTestPebbleStore(t)

	var wg sync.WaitGroup
	n := 50

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Appendf(nil, "key-%03d", i)
			val := fmt.Appendf(nil, "val-%03d", i)
			_ = s.Set(key, val)
		}(i)
	}
	wg.Wait()

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Appendf(nil, "key-%03d", i)
			val, err := s.Get(key)
			if err != nil {
				t.Errorf("Get %s: %v", key, err)
			}
			expected := fmt.Sprintf("val-%03d", i)
			if string(val) != expected {
				t.Errorf("key %s: expected %s, got %s", key, expected, val)
			}
		}(i)
	}
	wg.Wait()
}

func TestPebbleStore_FunctionalOptions(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir,
		WithMemTableSize(2<<20),
		WithL0CompactionThreshold(2),
		WithCacheSize(4<<20),
	)
	if err != nil {
		t.Fatalf("NewPebbleStore with options: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Verify it works with custom config.
	if err := s.Set([]byte("test"), []byte("value")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, err := s.Get([]byte("test"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "value" {
		t.Fatalf("expected value, got %s", val)
	}
}

func TestPrefixSuccessor(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		expect []byte
	}{
		{"normal", []byte("abc"), []byte("abd")},
		{"trailing_ff", []byte{0xFF}, nil},
		{"empty", []byte{}, nil},
		{"jobs/", []byte("jobs/"), []byte("jobs0")}, // '/' + 1 = '0'
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prefixSuccessor(tt.input)
			if string(got) != string(tt.expect) {
				t.Errorf("prefixSuccessor(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}
