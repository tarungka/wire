package coordinator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMemoryStore_GetSetDelete(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

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

func TestMemoryStore_WriteBatch(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

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

func TestMemoryStore_WriteBatch_Overwrite(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	if err := s.Set([]byte("x"), []byte("old")); err != nil {
		t.Fatal(err)
	}

	batch := []KVPair{
		{Key: []byte("x"), Value: []byte("new")},
		{Key: []byte("y"), Value: []byte("fresh")},
	}
	if err := s.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}

	val, _ := s.Get([]byte("x"))
	if string(val) != "new" {
		t.Fatalf("expected new, got %s", val)
	}
	val, _ = s.Get([]byte("y"))
	if string(val) != "fresh" {
		t.Fatalf("expected fresh, got %s", val)
	}
}

func TestMemoryStore_PrefixScan(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

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
	// Verify sorted order.
	for i := 1; i < len(keys); i++ {
		if keys[i] <= keys[i-1] {
			t.Fatalf("keys not in sorted order: %v", keys)
		}
	}
}

func TestMemoryStore_PrefixScan_EarlyStop(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	for i := range 10 {
		if err := s.Set(fmt.Appendf(nil, "prefix/%02d", i), []byte("x")); err != nil {
			t.Fatalf("Set: %v", err)
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

func TestMemoryStore_PrefixScan_EmptyPrefix(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	if err := s.Set([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set([]byte("b"), []byte("2")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	count := 0
	if err := s.PrefixScan([]byte(""), func(key, value []byte) bool {
		count++
		return true
	}); err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestMemoryStore_Snapshot(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	if err := s.Set([]byte("key1"), []byte("val1")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set([]byte("key2"), []byte("val2")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	dir := t.TempDir()
	snapDir := filepath.Join(dir, "snap")
	if err := s.Snapshot(snapDir); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(snapDir, "snapshot.json"))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	type entry struct {
		Key   string `json:"key"`
		Value []byte `json:"value"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Key != "key1" || string(entries[0].Value) != "val1" {
		t.Fatalf("unexpected entry[0]: %+v", entries[0])
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	var wg sync.WaitGroup
	n := 50

	// Concurrent writes.
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

	// Concurrent reads.
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

func TestMemoryStore_ClosedStoreErrors(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Set([]byte("k"), []byte("v"))
	_ = s.Close()

	if _, err := s.Get([]byte("k")); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
	if err := s.Set([]byte("k"), []byte("v")); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
	if err := s.Delete([]byte("k")); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
	if err := s.WriteBatch(nil); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
	if err := s.PrefixScan([]byte(""), nil); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
	if err := s.Snapshot(t.TempDir()); err != ErrStoreCorrupted {
		t.Fatalf("expected ErrStoreCorrupted, got %v", err)
	}
}

func TestMemoryStore_GetReturnsCopy(t *testing.T) {
	s := NewMemoryStore()
	defer func() { _ = s.Close() }()

	if err := s.Set([]byte("k"), []byte("original")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, _ := s.Get([]byte("k"))

	// Mutate the returned slice.
	val[0] = 'X'

	// The stored value should be unchanged.
	val2, _ := s.Get([]byte("k"))
	if string(val2) != "original" {
		t.Fatalf("expected original, got %s (mutation leaked)", val2)
	}
}
