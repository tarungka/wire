package coordinator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// MemoryStore is an in-memory MetadataStore implementation backed by a sorted
// slice of key-value entries. It is intended for testing and development.
type MemoryStore struct {
	mu      sync.RWMutex
	entries []kvEntry
	closed  bool
}

type kvEntry struct {
	key   string
	value []byte
}

// NewMemoryStore creates a new in-memory metadata store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (m *MemoryStore) Get(key []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrStoreCorrupted
	}
	k := string(key)
	idx := m.search(k)
	if idx < len(m.entries) && m.entries[idx].key == k {
		// Return a copy to prevent mutation.
		v := make([]byte, len(m.entries[idx].value))
		copy(v, m.entries[idx].value)
		return v, nil
	}
	return nil, nil
}

func (m *MemoryStore) Set(key, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStoreCorrupted
	}
	k := string(key)
	v := make([]byte, len(value))
	copy(v, value)

	idx := m.search(k)
	if idx < len(m.entries) && m.entries[idx].key == k {
		m.entries[idx].value = v
		return nil
	}
	// Insert in sorted order.
	m.entries = append(m.entries, kvEntry{})
	copy(m.entries[idx+1:], m.entries[idx:])
	m.entries[idx] = kvEntry{key: k, value: v}
	return nil
}

func (m *MemoryStore) Delete(key []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStoreCorrupted
	}
	k := string(key)
	idx := m.search(k)
	if idx < len(m.entries) && m.entries[idx].key == k {
		m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
	}
	return nil
}

func (m *MemoryStore) WriteBatch(batch []KVPair) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStoreCorrupted
	}
	for _, kv := range batch {
		k := string(kv.Key)
		v := make([]byte, len(kv.Value))
		copy(v, kv.Value)

		idx := m.search(k)
		if idx < len(m.entries) && m.entries[idx].key == k {
			m.entries[idx].value = v
		} else {
			m.entries = append(m.entries, kvEntry{})
			copy(m.entries[idx+1:], m.entries[idx:])
			m.entries[idx] = kvEntry{key: k, value: v}
		}
	}
	return nil
}

func (m *MemoryStore) PrefixScan(prefix []byte, fn func(key, value []byte) bool) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrStoreCorrupted
	}
	p := string(prefix)
	idx := m.search(p)
	for i := idx; i < len(m.entries); i++ {
		if !strings.HasPrefix(m.entries[i].key, p) {
			break
		}
		if !fn([]byte(m.entries[i].key), m.entries[i].value) {
			break
		}
	}
	return nil
}

func (m *MemoryStore) Snapshot(destDir string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return ErrStoreCorrupted
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	// Serialize entries to JSON.
	type entry struct {
		Key   string `json:"key"`
		Value []byte `json:"value"`
	}
	out := make([]entry, len(m.entries))
	for i, e := range m.entries {
		out[i] = entry{Key: e.key, Value: e.value}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, "snapshot.json"), data, 0o644)
}

func (m *MemoryStore) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.entries = nil
	return nil
}

// search returns the index where key would be inserted (binary search).
// Caller must hold at least a read lock.
func (m *MemoryStore) search(key string) int {
	return sort.Search(len(m.entries), func(i int) bool {
		return m.entries[i].key >= key
	})
}
