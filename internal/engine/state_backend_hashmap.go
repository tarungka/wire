package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

// hashMapSnapshotVersion is the binary format version for HashMap snapshots.
const hashMapSnapshotVersion uint8 = 1

// HashMapStateBackend is an in-memory state backend backed by a sorted slice.
// It is designed for testing, development, and small-state workloads where
// the full dataset fits in memory.
//
// Thread safety: all public methods are safe for concurrent use via a sync.RWMutex.
// Memory accounting: Put operations track curMemBytes and reject writes that
// would exceed memLimit (0 = unlimited).
type HashMapStateBackend struct {
	mu          sync.RWMutex
	entries     []kvEntry // sorted by key
	curMemBytes int64
	memLimit    int64 // 0 = unlimited
	closed      bool
}

// kvEntry is a key-value pair stored in sorted order.
type kvEntry struct {
	key   []byte
	value []byte
}

// NewHashMapStateBackend creates an in-memory state backend. memLimit sets the
// maximum memory usage in bytes; 0 means unlimited.
func NewHashMapStateBackend(memLimit int64) *HashMapStateBackend {
	return &HashMapStateBackend{
		memLimit: memLimit,
	}
}

// Put stores a key-value pair. If the key already exists, it is updated.
func (h *HashMapStateBackend) Put(key, value []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrBackendClosed
	}

	idx := h.search(key)

	if idx < len(h.entries) && bytes.Equal(h.entries[idx].key, key) {
		// Update existing entry.
		oldSize := int64(len(h.entries[idx].value))
		newSize := int64(len(value))
		delta := newSize - oldSize
		if h.memLimit > 0 && h.curMemBytes+delta > h.memLimit {
			return ErrMemoryLimitExceeded
		}
		h.entries[idx].value = cloneBytes(value)
		h.curMemBytes += delta
		return nil
	}

	// New entry.
	entrySize := int64(len(key) + len(value))
	if h.memLimit > 0 && h.curMemBytes+entrySize > h.memLimit {
		return ErrMemoryLimitExceeded
	}

	entry := kvEntry{key: cloneBytes(key), value: cloneBytes(value)}
	// Insert in sorted position.
	h.entries = append(h.entries, kvEntry{})
	copy(h.entries[idx+1:], h.entries[idx:])
	h.entries[idx] = entry
	h.curMemBytes += entrySize
	return nil
}

// Get retrieves the value for key. Returns a copy owned by the caller.
func (h *HashMapStateBackend) Get(key []byte) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return nil, ErrBackendClosed
	}

	idx := h.search(key)
	if idx < len(h.entries) && bytes.Equal(h.entries[idx].key, key) {
		return cloneBytes(h.entries[idx].value), nil
	}
	return nil, ErrKeyNotFound
}

// Delete removes a key.
func (h *HashMapStateBackend) Delete(key []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrBackendClosed
	}

	idx := h.search(key)
	if idx >= len(h.entries) || !bytes.Equal(h.entries[idx].key, key) {
		return ErrKeyNotFound
	}

	h.curMemBytes -= int64(len(h.entries[idx].key) + len(h.entries[idx].value))
	h.entries = append(h.entries[:idx], h.entries[idx+1:]...)
	return nil
}

// NewIterator returns an iterator over all keys with the given prefix.
func (h *HashMapStateBackend) NewIterator(prefix []byte) StateIterator {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return &hashMapIterator{} // empty iterator
	}

	// Find the start of the prefix range.
	start := sort.Search(len(h.entries), func(i int) bool {
		return bytes.Compare(h.entries[i].key, prefix) >= 0
	})

	// Collect all entries with the given prefix. We snapshot them to avoid
	// holding the lock during iteration.
	var snapshot []kvEntry
	for i := start; i < len(h.entries); i++ {
		if !bytes.HasPrefix(h.entries[i].key, prefix) {
			break
		}
		snapshot = append(snapshot, kvEntry{
			key:   cloneBytes(h.entries[i].key),
			value: cloneBytes(h.entries[i].value),
		})
	}

	return &hashMapIterator{entries: snapshot, pos: -1}
}

// Checkpoint creates a serialized snapshot of the current state.
//
// Binary format:
//
//	[version:1B][num_entries:4B LE][entries...][crc32:4B LE]
//
// Each entry:
//
//	[key_len:4B LE][key][value_len:4B LE][value]
func (h *HashMapStateBackend) Checkpoint(checkpointID uint64) (SnapshotHandle, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return SnapshotHandle{}, ErrBackendClosed
	}

	data, err := serializeHashMapSnapshot(h.entries)
	if err != nil {
		return SnapshotHandle{}, fmt.Errorf("hashmap checkpoint: %w", err)
	}

	return SnapshotHandle{
		CheckpointID: checkpointID,
		BackendType:  StateBackendHashMap,
		Data:         data,
	}, nil
}

// Restore replaces the backend state with the contents of a snapshot.
func (h *HashMapStateBackend) Restore(handle SnapshotHandle) error {
	entries, err := deserializeHashMapSnapshot(handle.Data)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrBackendClosed
	}

	// Compute new memory usage.
	var memBytes int64
	for _, e := range entries {
		memBytes += int64(len(e.key) + len(e.value))
	}
	if h.memLimit > 0 && memBytes > h.memLimit {
		return ErrMemoryLimitExceeded
	}

	h.entries = entries
	h.curMemBytes = memBytes
	return nil
}

// Close releases resources and marks the backend as closed.
func (h *HashMapStateBackend) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrBackendClosed
	}

	h.closed = true
	h.entries = nil
	h.curMemBytes = 0
	return nil
}

// MemUsage returns the current memory usage in bytes. Safe for concurrent use.
func (h *HashMapStateBackend) MemUsage() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.curMemBytes
}

// Len returns the number of entries. Safe for concurrent use.
func (h *HashMapStateBackend) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.entries)
}

// search returns the index where key would be inserted to maintain sorted order.
// Must be called with h.mu held (read or write).
func (h *HashMapStateBackend) search(key []byte) int {
	return sort.Search(len(h.entries), func(i int) bool {
		return bytes.Compare(h.entries[i].key, key) >= 0
	})
}

// cloneBytes returns a copy of b. Returns nil if b is nil.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// hashMapIterator iterates over a snapshot of entries.
type hashMapIterator struct {
	entries []kvEntry
	pos     int
}

func (it *hashMapIterator) Next() bool {
	it.pos++
	return it.pos < len(it.entries)
}

func (it *hashMapIterator) Key() []byte {
	if it.pos < 0 || it.pos >= len(it.entries) {
		return nil
	}
	return it.entries[it.pos].key
}

func (it *hashMapIterator) Value() []byte {
	if it.pos < 0 || it.pos >= len(it.entries) {
		return nil
	}
	return it.entries[it.pos].value
}

func (it *hashMapIterator) Close() {
	it.entries = nil
}

// serializeHashMapSnapshot encodes entries into the binary snapshot format.
func serializeHashMapSnapshot(entries []kvEntry) ([]byte, error) {
	// Pre-calculate buffer size.
	size := 1 + 4 // version + num_entries
	for _, e := range entries {
		size += 4 + len(e.key) + 4 + len(e.value)
	}
	size += 4 // CRC32

	buf := make([]byte, 0, size)

	// Version.
	buf = append(buf, hashMapSnapshotVersion)

	// Number of entries.
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(entries)))
	buf = append(buf, tmp[:]...)

	// Entries.
	for _, e := range entries {
		binary.LittleEndian.PutUint32(tmp[:], uint32(len(e.key)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, e.key...)

		binary.LittleEndian.PutUint32(tmp[:], uint32(len(e.value)))
		buf = append(buf, tmp[:]...)
		buf = append(buf, e.value...)
	}

	// CRC32 over everything before the checksum.
	checksum := crc32.ChecksumIEEE(buf)
	binary.LittleEndian.PutUint32(tmp[:], checksum)
	buf = append(buf, tmp[:]...)

	return buf, nil
}

// deserializeHashMapSnapshot decodes entries from the binary snapshot format.
func deserializeHashMapSnapshot(data []byte) ([]kvEntry, error) {
	if len(data) < 9 { // version(1) + num_entries(4) + crc32(4)
		return nil, fmt.Errorf("%w: snapshot too short (%d bytes)", ErrSnapshotCorrupt, len(data))
	}

	// Verify CRC32.
	payloadLen := len(data) - 4
	expected := binary.LittleEndian.Uint32(data[payloadLen:])
	actual := crc32.ChecksumIEEE(data[:payloadLen])
	if expected != actual {
		return nil, fmt.Errorf("%w: CRC32 mismatch (expected %08x, got %08x)", ErrSnapshotCorrupt, expected, actual)
	}

	// Version check.
	if data[0] != hashMapSnapshotVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrSnapshotCorrupt, data[0])
	}

	numEntries := binary.LittleEndian.Uint32(data[1:5])
	pos := 5

	entries := make([]kvEntry, 0, numEntries)
	for i := uint32(0); i < numEntries; i++ {
		if pos+4 > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d key length", ErrSnapshotCorrupt, i)
		}
		keyLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pos+keyLen > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d key data", ErrSnapshotCorrupt, i)
		}
		key := make([]byte, keyLen)
		copy(key, data[pos:pos+keyLen])
		pos += keyLen

		if pos+4 > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d value length", ErrSnapshotCorrupt, i)
		}
		valLen := int(binary.LittleEndian.Uint32(data[pos : pos+4]))
		pos += 4

		if pos+valLen > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d value data", ErrSnapshotCorrupt, i)
		}
		value := make([]byte, valLen)
		copy(value, data[pos:pos+valLen])
		pos += valLen

		entries = append(entries, kvEntry{key: key, value: value})
	}

	return entries, nil
}
