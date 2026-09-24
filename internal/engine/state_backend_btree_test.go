package engine

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestHashMapOrderedSnapshotCompatibility(t *testing.T) {
	backend := NewHashMapStateBackend(0)
	entries := make([]kvEntry, 10000)
	for i := len(entries) - 1; i >= 0; i-- {
		key := []byte(fmt.Sprintf("key/%05d", i))
		value := []byte(fmt.Sprintf("value/%d", i))
		entries[i] = kvEntry{key: key, value: value}
		if err := backend.Put(key, value); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := serializeHashMapSnapshot(entries)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := backend.Checkpoint(1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(handle.Data, expected) {
		t.Fatal("snapshot differs from sorted encoding")
	}
	restored := NewHashMapStateBackend(0)
	if err := restored.Restore(handle); err != nil {
		t.Fatal(err)
	}
	it := restored.NewIterator([]byte("key/000"))
	defer it.Close()
	// Iterators retain their original view through subsequent mutations.
	if err := restored.Delete([]byte("key/00000")); err != nil {
		t.Fatal(err)
	}
	if err := restored.Put([]byte("key/00001"), []byte("changed")); err != nil {
		t.Fatal(err)
	}
	count := 0
	for it.Next() {
		if !bytes.Equal(it.Key(), entries[count].key) || !bytes.Equal(it.Value(), entries[count].value) {
			t.Fatalf("unexpected entry %d", count)
		}
		count++
	}
	if count != 100 {
		t.Fatalf("prefix entries = %d, want 100", count)
	}
}

func TestHashMapRejectsUnorderedSnapshotAtomically(t *testing.T) {
	for _, keys := range [][]string{{"b", "a"}, {"a", "a"}} {
		t.Run(fmt.Sprint(keys), func(t *testing.T) {
			backend := NewHashMapStateBackend(0)
			if err := backend.Put([]byte("existing"), []byte("value")); err != nil {
				t.Fatal(err)
			}
			originalUsage := backend.MemUsage()
			entries := []kvEntry{{key: []byte(keys[0])}, {key: []byte(keys[1])}}
			data, err := serializeHashMapSnapshot(entries)
			if err != nil {
				t.Fatal(err)
			}
			err = backend.Restore(SnapshotHandle{BackendType: StateBackendHashMap, Data: data})
			if !errors.Is(err, ErrSnapshotCorrupt) {
				t.Fatalf("restore error = %v", err)
			}
			value, err := backend.Get([]byte("existing"))
			if err != nil || string(value) != "value" || backend.Len() != 1 || backend.MemUsage() != originalUsage {
				t.Fatal("rejected restore changed state")
			}
		})
	}
}

func TestHashMapBatchMemoryLimitIsAtomic(t *testing.T) {
	backend := NewHashMapStateBackend(4)
	if err := backend.Put([]byte("a"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	err := backend.ApplyBatch([]StateMutation{
		{Key: []byte("a"), Delete: true},
		{Key: []byte("b"), Value: []byte("large")},
	})
	if !errors.Is(err, ErrMemoryLimitExceeded) {
		t.Fatalf("batch error = %v", err)
	}
	value, err := backend.Get([]byte("a"))
	if err != nil || string(value) != "old" || backend.MemUsage() != 4 || backend.Len() != 1 {
		t.Fatal("rejected batch changed state")
	}
	// Validate final usage, not intermediate usage; repeated mutations count once.
	err = backend.ApplyBatch([]StateMutation{
		{Key: []byte("b"), Value: []byte("large")},
		{Key: []byte("a"), Delete: true},
		{Key: []byte("b"), Value: []byte("new")},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err = backend.Get([]byte("b"))
	if err != nil || string(value) != "new" || backend.MemUsage() != 4 || backend.Len() != 1 {
		t.Fatal("incorrect committed batch")
	}
	if _, err := backend.Get([]byte("a")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatal("deleted key remains")
	}
}
