package engine

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sync"

	"github.com/tidwall/btree"
)

// hashMapSnapshotVersion is the binary format version for HashMap snapshots.
const hashMapSnapshotVersion uint8 = 1

const hashMapSnapshotMagic = "WHSB"

// HashMapStateBackend is an in-memory state backend backed by a B-tree.
// It is designed for testing, development, and small-state workloads where
// the full dataset fits in memory.
//
// Thread safety: all public methods are safe for concurrent use via a sync.RWMutex.
// Memory accounting: Put operations track curMemBytes and reject writes that
// would exceed memLimit (0 = unlimited).
type HashMapStateBackend struct {
	mu          sync.RWMutex
	entries     *btree.BTreeG[kvEntry] // ordered by key; guarded by mu
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
		entries:  newHashMapTree(),
	}
}

// Put stores a key-value pair. If the key already exists, it is updated.
func (h *HashMapStateBackend) Put(key, value []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrBackendClosed
	}

	previous, exists := h.entries.Get(kvEntry{key: key})
	if exists {
		delta := int64(len(value)) - int64(len(previous.value))
		if h.memLimit > 0 && h.curMemBytes+delta > h.memLimit {
			return ErrMemoryLimitExceeded
		}
		h.entries.Set(kvEntry{key: previous.key, value: cloneBytes(value)})
		h.curMemBytes += delta
		return nil
	}

	// New entry.
	entrySize := int64(len(key) + len(value))
	if h.memLimit > 0 && h.curMemBytes+entrySize > h.memLimit {
		return ErrMemoryLimitExceeded
	}

	entry := kvEntry{key: cloneBytes(key), value: cloneBytes(value)}
	h.entries.Set(entry)
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

	if entry, ok := h.entries.Get(kvEntry{key: key}); ok {
		return cloneBytes(entry.value), nil
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

	entry, ok := h.entries.Delete(kvEntry{key: key})
	if !ok {
		return ErrKeyNotFound
	}
	h.curMemBytes -= int64(len(entry.key) + len(entry.value))
	return nil
}

// NewIterator returns an iterator over all keys with the given prefix.
func (h *HashMapStateBackend) NewIterator(prefix []byte) StateIterator {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.closed {
		return &hashMapIterator{} // empty iterator
	}

	// Copy matching entries so iteration neither holds the lock nor aliases state.
	var snapshot []kvEntry
	h.entries.Ascend(kvEntry{key: prefix}, func(entry kvEntry) bool {
		if !bytes.HasPrefix(entry.key, prefix) {
			return false
		}
		snapshot = append(snapshot, kvEntry{key: cloneBytes(entry.key), value: cloneBytes(entry.value)})
		return true
	})

	return &hashMapIterator{entries: snapshot, pos: -1}
}

// Checkpoint creates a serialized snapshot of the current state.
//
// Binary format:
//
//	[magic:4B WHSB][version:1B][num_entries:4B LE][entries...][crc32:4B LE]
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

	entries := make([]kvEntry, 0, h.entries.Len())
	h.entries.Scan(func(entry kvEntry) bool {
		entries = append(entries, entry)
		return true
	})
	data, err := serializeHashMapSnapshot(entries)
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
	if handle.BackendType != StateBackendHashMap {
		return fmt.Errorf("%w: expected %q, got %q", ErrSnapshotCorrupt, StateBackendHashMap, handle.BackendType)
	}

	entries, err := deserializeHashMapSnapshot(handle.Data)
	if err != nil {
		return fmt.Errorf("hashmap restore: %w", err)
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

	tree := newHashMapTree()
	for i, entry := range entries {
		if i > 0 && bytes.Compare(entries[i-1].key, entry.key) >= 0 {
			return fmt.Errorf("%w: snapshot keys must be strictly increasing", ErrSnapshotCorrupt)
		}
		tree.Set(entry)
	}
	h.entries = tree
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
	if h.closed {
		return 0
	}
	return h.entries.Len()
}

// The backend lock makes tree updates and memory accounting atomic together.
func newHashMapTree() *btree.BTreeG[kvEntry] {
	return btree.NewBTreeGOptions(func(a, b kvEntry) bool {
		return bytes.Compare(a.key, b.key) < 0
	}, btree.Options{NoLocks: true})
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
	size := len(hashMapSnapshotMagic) + 1 + 4 // magic + version + num_entries
	for _, e := range entries {
		size += 4 + len(e.key) + 4 + len(e.value)
	}
	size += 4 // CRC32

	buf := make([]byte, 0, size)

	buf = append(buf, hashMapSnapshotMagic...)
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

	// New snapshots carry the proposal's magic header. The old unframed
	// version-1 format remains readable for checkpoint/savepoint upgrades.
	header := 0
	if bytes.HasPrefix(data, []byte(hashMapSnapshotMagic)) {
		header = len(hashMapSnapshotMagic)
		if payloadLen < header+5 {
			return nil, fmt.Errorf("%w: truncated snapshot header", ErrSnapshotCorrupt)
		}
	} else if data[0] != hashMapSnapshotVersion {
		return nil, fmt.Errorf("%w: invalid snapshot magic", ErrSnapshotCorrupt)
	}
	if data[header] != hashMapSnapshotVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrSnapshotCorrupt, data[header])
	}

	numEntries := binary.LittleEndian.Uint32(data[header+1 : header+5])
	pos := header + 5
	// Each entry needs at least its two lengths. Compare without narrowing
	// the payload size so oversized headers cannot trigger a huge allocation.
	if uint64(numEntries) > uint64(payloadLen-pos)/8 {
		return nil, fmt.Errorf("%w: numEntries %d exceeds payload capacity", ErrSnapshotCorrupt, numEntries)
	}

	entries := make([]kvEntry, 0, numEntries)
	for i := uint32(0); i < numEntries; i++ {
		if pos+4 > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d key length", ErrSnapshotCorrupt, i)
		}
		keySize := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4

		if uint64(keySize) > uint64(payloadLen-pos) {
			return nil, fmt.Errorf("%w: truncated at entry %d key data", ErrSnapshotCorrupt, i)
		}
		keyLen := int(keySize)
		key := make([]byte, keyLen)
		copy(key, data[pos:pos+keyLen])
		pos += keyLen

		if pos+4 > payloadLen {
			return nil, fmt.Errorf("%w: truncated at entry %d value length", ErrSnapshotCorrupt, i)
		}
		valueSize := binary.LittleEndian.Uint32(data[pos : pos+4])
		pos += 4

		if uint64(valueSize) > uint64(payloadLen-pos) {
			return nil, fmt.Errorf("%w: truncated at entry %d value data", ErrSnapshotCorrupt, i)
		}
		valLen := int(valueSize)
		value := make([]byte, valLen)
		copy(value, data[pos:pos+valLen])
		pos += valLen

		entries = append(entries, kvEntry{key: key, value: value})
	}

	if pos != payloadLen {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrSnapshotCorrupt, payloadLen-pos)
	}

	return entries, nil
}
