package engine

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cockroachdb/pebble"

	"github.com/tarungka/wire/internal/keygroup"
)

// VisitKeyGroupRange scans the half-open key-group interval efficiently using
// Pebble bounds. The visitor must not call this backend; key/value slices are
// valid only during the callback. Use on a restored immutable savepoint when
// assembling rescaled state. Cancellation and iterator errors are propagated.
func (b *PebbleStateBackend) VisitKeyGroupRange(ctx context.Context, groups keygroup.KeyGroupRange, visit func(key, value []byte) error) error {
	if groups.Start >= groups.End || int(groups.End) > keygroup.MaxKeyGroups {
		return fmt.Errorf("invalid key-group range [%d,%d)", groups.Start, groups.End)
	}
	if visit == nil {
		return fmt.Errorf("nil key-group visitor")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	var lower, upper [2]byte
	binary.BigEndian.PutUint16(lower[:], groups.Start)
	binary.BigEndian.PutUint16(upper[:], groups.End)
	iter, err := b.db.NewIter(&pebble.IterOptions{LowerBound: lower[:], UpperBound: upper[:]})
	if err != nil {
		return err
	}
	defer iter.Close()
	for valid := iter.First(); valid; valid = iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(iter.Key(), iter.Value()); err != nil {
			return err
		}
	}
	return iter.Error()
}
