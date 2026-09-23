package engine

import (
	"sort"

	"github.com/cockroachdb/pebble"
)

// StateMutation is one operation in an atomic operator-state update.
type StateMutation struct {
	Key, Value []byte
	Delete     bool
}

// BatchedStateBackend atomically updates window records and their watermark.
// The built-in backends implement it. A failed batch leaves old state intact.
type BatchedStateBackend interface {
	StateBackend
	ApplyBatch([]StateMutation) error
}

func (b *PebbleStateBackend) ApplyBatch(changes []StateMutation) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	batch := b.db.NewBatch()
	defer batch.Close()
	for _, change := range changes {
		var err error
		if change.Delete {
			err = batch.Delete(change.Key, nil)
		} else {
			err = batch.Set(change.Key, change.Value, nil)
		}
		if err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

func (h *HashMapStateBackend) ApplyBatch(changes []StateMutation) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrBackendClosed
	}
	// Preserve immutable existing payloads; clone only incoming values. Build a
	// replacement index before publication so limits/errors cannot half-apply.
	entries := make(map[string]kvEntry, len(h.entries)+len(changes))
	for _, entry := range h.entries {
		entries[string(entry.key)] = entry
	}
	for _, change := range changes {
		if change.Delete {
			delete(entries, string(change.Key))
		} else {
			entries[string(change.Key)] = kvEntry{key: cloneBytes(change.Key), value: cloneBytes(change.Value)}
		}
	}
	var size int64
	next := make([]kvEntry, 0, len(entries))
	for _, entry := range entries {
		size += int64(len(entry.key) + len(entry.value))
		next = append(next, entry)
	}
	if h.memLimit > 0 && size > h.memLimit {
		return ErrMemoryLimitExceeded
	}
	sort.Slice(next, func(i, j int) bool { return string(next[i].key) < string(next[j].key) })
	h.entries, h.curMemBytes = next, size
	return nil
}
