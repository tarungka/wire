package engine

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// -- StateBackend contract tests (run against all implementations) --

// stateBackendFactory creates a fresh StateBackend for testing.
type stateBackendFactory func(t *testing.T) StateBackend

// runStateBackendContractTests validates the StateBackend contract against
// any implementation.
func runStateBackendContractTests(t *testing.T, name string, factory stateBackendFactory) {
	t.Run(name+"/PutGetDelete", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		// Put and Get.
		if err := b.Put([]byte("key1"), []byte("val1")); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, err := b.Get([]byte("key1"))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !bytes.Equal(got, []byte("val1")) {
			t.Errorf("Get: got %q, want %q", got, "val1")
		}

		// Update existing key.
		if err := b.Put([]byte("key1"), []byte("val2")); err != nil {
			t.Fatalf("Put update: %v", err)
		}
		got, err = b.Get([]byte("key1"))
		if err != nil {
			t.Fatalf("Get after update: %v", err)
		}
		if !bytes.Equal(got, []byte("val2")) {
			t.Errorf("Get after update: got %q, want %q", got, "val2")
		}

		// Delete.
		if err := b.Delete([]byte("key1")); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err = b.Get([]byte("key1"))
		if !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("Get after delete: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run(name+"/GetMissing", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		_, err := b.Get([]byte("nonexistent"))
		if !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("Get missing: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run(name+"/DeleteMissing", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		err := b.Delete([]byte("nonexistent"))
		if !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("Delete missing: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run(name+"/EmptyKeyValue", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		// Empty key with non-empty value.
		if err := b.Put([]byte{}, []byte("val")); err != nil {
			t.Fatalf("Put empty key: %v", err)
		}
		got, err := b.Get([]byte{})
		if err != nil {
			t.Fatalf("Get empty key: %v", err)
		}
		if !bytes.Equal(got, []byte("val")) {
			t.Errorf("Get empty key: got %q, want %q", got, "val")
		}

		// Non-empty key with empty value.
		if err := b.Put([]byte("k"), []byte{}); err != nil {
			t.Fatalf("Put empty value: %v", err)
		}
		got, err = b.Get([]byte("k"))
		if err != nil {
			t.Fatalf("Get empty value: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Get empty value: got %q, want empty", got)
		}
	})

	t.Run(name+"/Iterator", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		// Insert entries with different prefixes.
		entries := [][2]string{
			{"user:1", "alice"},
			{"user:2", "bob"},
			{"user:3", "carol"},
			{"order:1", "ord-a"},
			{"order:2", "ord-b"},
		}
		for _, e := range entries {
			if err := b.Put([]byte(e[0]), []byte(e[1])); err != nil {
				t.Fatalf("Put %s: %v", e[0], err)
			}
		}

		// Iterate with prefix "user:".
		it := b.NewIterator([]byte("user:"))
		var got []string
		for it.Next() {
			got = append(got, string(it.Key()))
		}
		it.Close()

		want := []string{"user:1", "user:2", "user:3"}
		if len(got) != len(want) {
			t.Fatalf("Iterator user: got %d entries, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Iterator user[%d]: got %q, want %q", i, got[i], want[i])
			}
		}

		// Iterate with prefix "order:".
		it = b.NewIterator([]byte("order:"))
		got = nil
		for it.Next() {
			got = append(got, string(it.Key()))
		}
		it.Close()

		want = []string{"order:1", "order:2"}
		if len(got) != len(want) {
			t.Fatalf("Iterator order: got %d entries, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Iterator order[%d]: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run(name+"/IteratorEmptyPrefix", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		if err := b.Put([]byte("a"), []byte("1")); err != nil {
			t.Fatal(err)
		}
		if err := b.Put([]byte("b"), []byte("2")); err != nil {
			t.Fatal(err)
		}

		// Empty prefix should return all entries.
		it := b.NewIterator([]byte{})
		count := 0
		for it.Next() {
			count++
		}
		it.Close()

		if count != 2 {
			t.Errorf("Iterator empty prefix: got %d entries, want 2", count)
		}
	})

	t.Run(name+"/IteratorNoMatch", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		if err := b.Put([]byte("abc"), []byte("1")); err != nil {
			t.Fatal(err)
		}

		it := b.NewIterator([]byte("xyz"))
		if it.Next() {
			t.Error("Iterator expected no entries for non-matching prefix")
		}
		it.Close()
	})

	t.Run(name+"/CheckpointRestore", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		// Populate state.
		for i := 0; i < 10; i++ {
			key := fmt.Sprintf("key-%03d", i)
			val := fmt.Sprintf("val-%03d", i)
			if err := b.Put([]byte(key), []byte(val)); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}

		// Checkpoint.
		handle, err := b.Checkpoint(42)
		if err != nil {
			t.Fatalf("Checkpoint: %v", err)
		}
		if handle.CheckpointID != 42 {
			t.Errorf("CheckpointID: got %d, want 42", handle.CheckpointID)
		}

		// Modify state after checkpoint.
		if err := b.Put([]byte("key-000"), []byte("modified")); err != nil {
			t.Fatalf("Put after checkpoint: %v", err)
		}
		if err := b.Put([]byte("new-key"), []byte("new-val")); err != nil {
			t.Fatalf("Put new key: %v", err)
		}

		// Restore from checkpoint.
		if err := b.Restore(handle); err != nil {
			t.Fatalf("Restore: %v", err)
		}

		// Verify restored state matches pre-checkpoint state.
		got, err := b.Get([]byte("key-000"))
		if err != nil {
			t.Fatalf("Get after restore: %v", err)
		}
		if !bytes.Equal(got, []byte("val-000")) {
			t.Errorf("key-000 after restore: got %q, want %q", got, "val-000")
		}

		// New key should not exist.
		_, err = b.Get([]byte("new-key"))
		if !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("new-key after restore: got %v, want ErrKeyNotFound", err)
		}
	})

	t.Run(name+"/ClosedBackend", func(t *testing.T) {
		b := factory(t)
		b.Close()

		if _, err := b.Get([]byte("k")); !errors.Is(err, ErrBackendClosed) {
			t.Errorf("Get on closed: got %v, want ErrBackendClosed", err)
		}
		if err := b.Put([]byte("k"), []byte("v")); !errors.Is(err, ErrBackendClosed) {
			t.Errorf("Put on closed: got %v, want ErrBackendClosed", err)
		}
		if err := b.Delete([]byte("k")); !errors.Is(err, ErrBackendClosed) {
			t.Errorf("Delete on closed: got %v, want ErrBackendClosed", err)
		}
		if _, err := b.Checkpoint(1); !errors.Is(err, ErrBackendClosed) {
			t.Errorf("Checkpoint on closed: got %v, want ErrBackendClosed", err)
		}
		if err := b.Close(); !errors.Is(err, ErrBackendClosed) {
			t.Errorf("Double close: got %v, want ErrBackendClosed", err)
		}
	})

	t.Run(name+"/ConcurrentAccess", func(t *testing.T) {
		b := factory(t)
		defer b.Close()

		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					key := []byte(fmt.Sprintf("g%d-k%d", id, i))
					val := []byte(fmt.Sprintf("v%d", i))
					_ = b.Put(key, val)
					_, _ = b.Get(key)
				}
			}(g)
		}
		wg.Wait()
	})
}

// -- HashMap-specific tests --

func TestHashMapStateBackend_Contract(t *testing.T) {
	runStateBackendContractTests(t, "HashMap", func(t *testing.T) StateBackend {
		return NewHashMapStateBackend(0) // unlimited
	})
}

func TestHashMapStateBackend_MemoryLimit(t *testing.T) {
	// Limit of 20 bytes.
	b := NewHashMapStateBackend(20)
	defer b.Close()

	// 10 bytes: "key1"(4) + "val1xx"(6) = 10.
	if err := b.Put([]byte("key1"), []byte("val1xx")); err != nil {
		t.Fatalf("Put within limit: %v", err)
	}

	// 10 more bytes: "key2"(4) + "val2xx"(6) = 10. Total = 20.
	if err := b.Put([]byte("key2"), []byte("val2xx")); err != nil {
		t.Fatalf("Put at limit: %v", err)
	}

	// 1 more byte would exceed.
	err := b.Put([]byte("key3"), []byte("v"))
	if !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Errorf("Put over limit: got %v, want ErrMemoryLimitExceeded", err)
	}

	// Update existing key to smaller value (should succeed).
	if err := b.Put([]byte("key1"), []byte("x")); err != nil {
		t.Fatalf("Put shrink: %v", err)
	}

	// Now there's room for a new key.
	if err := b.Put([]byte("k3"), []byte("v")); err != nil {
		t.Fatalf("Put after shrink: %v", err)
	}
}

func TestHashMapStateBackend_MemUsage(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	if b.MemUsage() != 0 {
		t.Fatalf("initial mem: got %d, want 0", b.MemUsage())
	}

	b.Put([]byte("abc"), []byte("12345"))
	if b.MemUsage() != 8 { // 3 + 5
		t.Errorf("after put: got %d, want 8", b.MemUsage())
	}

	b.Delete([]byte("abc"))
	if b.MemUsage() != 0 {
		t.Errorf("after delete: got %d, want 0", b.MemUsage())
	}
}

func TestHashMapStateBackend_SortedOrder(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	// Insert out of order.
	keys := []string{"charlie", "alpha", "bravo", "delta"}
	for _, k := range keys {
		b.Put([]byte(k), []byte(k))
	}

	it := b.NewIterator([]byte{})
	var got []string
	for it.Next() {
		got = append(got, string(it.Key()))
	}
	it.Close()

	want := []string{"alpha", "bravo", "charlie", "delta"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHashMapStateBackend_SnapshotCorrupt(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	b.Put([]byte("k"), []byte("v"))
	handle, err := b.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}

	// Corrupt the data.
	handle.Data[len(handle.Data)-1] ^= 0xFF

	err = b.Restore(handle)
	if !errors.Is(err, ErrSnapshotCorrupt) {
		t.Errorf("Restore corrupt: got %v, want ErrSnapshotCorrupt", err)
	}
}

func TestHashMapStateBackend_SnapshotTooShort(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	err := b.Restore(SnapshotHandle{Data: []byte{1, 2, 3}})
	if !errors.Is(err, ErrSnapshotCorrupt) {
		t.Errorf("Restore too short: got %v, want ErrSnapshotCorrupt", err)
	}
}

func TestHashMapStateBackend_EmptyCheckpointRestore(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	// Checkpoint empty state.
	handle, err := b.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}

	// Add data.
	b.Put([]byte("k"), []byte("v"))

	// Restore to empty.
	if err := b.Restore(handle); err != nil {
		t.Fatal(err)
	}

	_, err = b.Get([]byte("k"))
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Get after empty restore: got %v, want ErrKeyNotFound", err)
	}
}

func TestHashMapStateBackend_RestoreRespectsMemLimit(t *testing.T) {
	b := NewHashMapStateBackend(0) // no limit for checkpoint
	defer b.Close()

	// Create a large snapshot.
	for i := 0; i < 100; i++ {
		b.Put([]byte(fmt.Sprintf("key-%03d", i)), bytes.Repeat([]byte("x"), 100))
	}
	handle, err := b.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()

	// New backend with a small limit.
	b2 := NewHashMapStateBackend(50)
	defer b2.Close()

	err = b2.Restore(handle)
	if !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Errorf("Restore over limit: got %v, want ErrMemoryLimitExceeded", err)
	}
}

func TestHashMapStateBackend_Len(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	if b.Len() != 0 {
		t.Fatalf("initial len: got %d, want 0", b.Len())
	}

	b.Put([]byte("a"), []byte("1"))
	b.Put([]byte("b"), []byte("2"))
	if b.Len() != 2 {
		t.Errorf("after 2 puts: got %d, want 2", b.Len())
	}

	b.Put([]byte("a"), []byte("updated"))
	if b.Len() != 2 {
		t.Errorf("after update: got %d, want 2 (no duplicate)", b.Len())
	}

	b.Delete([]byte("a"))
	if b.Len() != 1 {
		t.Errorf("after delete: got %d, want 1", b.Len())
	}
}

// -- Factory tests --

func TestNewStateBackend_HashMap(t *testing.T) {
	b, err := NewStateBackend(StateBackendConfig{
		Type:            StateBackendHashMap,
		HashMapMemLimit: 1024,
	})
	if err != nil {
		t.Fatalf("NewStateBackend hashmap: %v", err)
	}
	defer b.Close()

	if err := b.Put([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := b.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("Get: got %q, want %q", got, "v")
	}
}

func TestNewStateBackend_UnknownType(t *testing.T) {
	_, err := NewStateBackend(StateBackendConfig{Type: "rocksdb"})
	if !errors.Is(err, ErrUnknownBackendType) {
		t.Errorf("NewStateBackend unknown: got %v, want ErrUnknownBackendType", err)
	}
}

func TestNewStateBackend_PebbleRequiresDir(t *testing.T) {
	_, err := NewStateBackend(StateBackendConfig{Type: StateBackendPebble})
	if err == nil {
		t.Error("NewStateBackend pebble without dir: expected error")
	}
}

func TestDefaultStateBackendConfig(t *testing.T) {
	cfg := DefaultStateBackendConfig()
	if cfg.Type != DefaultStateBackendType {
		t.Errorf("default type: got %q, want %q", cfg.Type, DefaultStateBackendType)
	}
}

// -- NoopStateBackendMetrics --

func TestNoopStateBackendMetrics(t *testing.T) {
	m := NoopStateBackendMetrics()
	// Should not panic.
	m.ObserveStateSize("hashmap", 1024)
	m.IncCheckpointTotal("hashmap")
	m.IncRestoreTotal("hashmap")
}

// -- Iterator isolation (mutations during iteration) --

func TestHashMapStateBackend_IteratorIsolation(t *testing.T) {
	b := NewHashMapStateBackend(0)
	defer b.Close()

	b.Put([]byte("a"), []byte("1"))
	b.Put([]byte("b"), []byte("2"))
	b.Put([]byte("c"), []byte("3"))

	it := b.NewIterator([]byte{})

	// Mutate the backend during iteration.
	b.Put([]byte("d"), []byte("4"))
	b.Delete([]byte("a"))

	// Iterator should see the snapshot at creation time.
	var keys []string
	for it.Next() {
		keys = append(keys, string(it.Key()))
	}
	it.Close()

	want := []string{"a", "b", "c"}
	if len(keys) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(keys), len(want), keys)
	}
	for i, w := range want {
		if keys[i] != w {
			t.Errorf("[%d]: got %q, want %q", i, keys[i], w)
		}
	}
}
