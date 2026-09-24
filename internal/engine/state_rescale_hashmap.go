package engine

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/tarungka/wire/internal/keygroup"
)

// VisitKeyGroupRange visits ordered keys in a half-open interval while holding
// a read lock. The visitor must not mutate slices or call back into this backend.
func (h *HashMapStateBackend) VisitKeyGroupRange(ctx context.Context, groups keygroup.KeyGroupRange, visit func([]byte, []byte) error) error {
	if groups.Start >= groups.End || int(groups.End) > keygroup.MaxKeyGroups || visit == nil {
		return fmt.Errorf("invalid key-group range or nil visitor")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return ErrBackendClosed
	}
	var lower [2]byte
	binary.BigEndian.PutUint16(lower[:], groups.Start)
	var result error
	h.entries.Ascend(kvEntry{key: lower[:]}, func(entry kvEntry) bool {
		if result = ctx.Err(); result != nil {
			return false
		}
		if len(entry.key) < 2 {
			result = fmt.Errorf("invalid key-group key")
			return false
		}
		if binary.BigEndian.Uint16(entry.key) >= groups.End {
			return false
		}
		result = visit(entry.key, entry.value)
		return result == nil
	})
	return result
}

// RestoreKeyGroupRanges publishes only after all source snapshots, ownership
// intervals and the destination memory limit validate. Failure leaves state intact.
func (h *HashMapStateBackend) RestoreKeyGroupRanges(ctx context.Context, assigned keygroup.KeyGroupRange, parts []KeyGroupSnapshot) error {
	ordered, _, err := validateKeyGroupSnapshots(assigned, parts)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.RLock()
	if h.closed {
		h.mu.RUnlock()
		return ErrBackendClosed
	}
	limit := h.memLimit
	h.mu.RUnlock()
	target := NewHashMapStateBackend(limit)
	defer target.Close()
	for _, part := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := NewHashMapStateBackend(0)
		err := source.Restore(part.Snapshot)
		if err == nil {
			err = source.VisitKeyGroupRange(ctx, part.Groups, target.Put)
		}
		_ = source.Close()
		if err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrBackendClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.entries, h.curMemBytes = target.entries, target.curMemBytes
	// Transfer ownership; Close on the temporary backend must not retain the tree.
	target.entries = nil
	target.curMemBytes = 0
	return nil
}
