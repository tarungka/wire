package engine

import "github.com/cockroachdb/pebble"

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
	// Copy-on-write keeps existing payloads immutable and publishes the entire
	// batch only after validating its final memory usage.
	next := h.entries.Copy()
	size := h.curMemBytes
	for _, change := range changes {
		if change.Delete {
			if old, ok := next.Delete(kvEntry{key: change.Key}); ok {
				size -= int64(len(old.key) + len(old.value))
			}
		} else {
			entry := kvEntry{key: cloneBytes(change.Key), value: cloneBytes(change.Value)}
			if old, replaced := next.Set(entry); replaced {
				size -= int64(len(old.key) + len(old.value))
			}
			size += int64(len(entry.key) + len(entry.value))
		}
	}
	if h.memLimit > 0 && size > h.memLimit {
		return ErrMemoryLimitExceeded
	}
	h.entries, h.curMemBytes = next, size
	return nil
}
