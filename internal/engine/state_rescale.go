package engine

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/tarungka/wire/internal/keygroup"
)

// KeyGroupSnapshot selects an interval from an immutable source snapshot.
type KeyGroupSnapshot struct {
	Groups   keygroup.KeyGroupRange
	Snapshot SnapshotHandle
}

// RestoreKeyGroupRanges assembles a complete replacement off to the side, then
// publishes through Restore's generation switch. Failed input verification or
// range copying leaves the destination unchanged. Parts must cover the entire
// assigned interval exactly once and belong to the same checkpoint.
func (b *PebbleStateBackend) RestoreKeyGroupRanges(ctx context.Context, assigned keygroup.KeyGroupRange, parts []KeyGroupSnapshot) error {
	ordered, checkpoint, err := validateKeyGroupSnapshots(assigned, parts)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	if b.db == nil {
		b.mu.Unlock()
		return ErrBackendClosed
	}
	root, compactions := b.root, b.maxCompactions
	b.mu.Unlock()
	staging, err := os.MkdirTemp(root, "rescale-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	target, err := newPebbleStateBackend(StateBackendConfig{PebbleDataDir: staging + "/merged", PebbleMaxCompactionConcurrency: compactions})
	if err != nil {
		return err
	}
	defer target.Close()
	for i, part := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := newPebbleStateBackend(StateBackendConfig{PebbleDataDir: fmt.Sprintf("%s/source-%d", staging, i), PebbleMaxCompactionConcurrency: compactions})
		if err != nil {
			return err
		}
		err = source.Restore(part.Snapshot)
		if err == nil {
			err = source.(*PebbleStateBackend).VisitKeyGroupRange(ctx, part.Groups, target.Put)
		}
		closeErr := source.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	handle, err := target.Checkpoint(checkpoint)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Restore(handle)
}

// validateKeyGroupSnapshots rejects incomplete or ambiguous ownership before
// either backend allocates a replacement. It never sorts the caller's slice.
func validateKeyGroupSnapshots(assigned keygroup.KeyGroupRange, parts []KeyGroupSnapshot) ([]KeyGroupSnapshot, uint64, error) {
	if assigned.Start >= assigned.End || int(assigned.End) > keygroup.MaxKeyGroups || len(parts) == 0 {
		return nil, 0, fmt.Errorf("invalid rescale range or empty snapshot set")
	}
	ordered := append([]KeyGroupSnapshot(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Groups.Start < ordered[j].Groups.Start })
	next := assigned.Start
	checkpoint := ordered[0].Snapshot.CheckpointID
	for _, part := range ordered {
		if part.Groups.Start != next || part.Groups.End <= next || part.Groups.End > assigned.End || part.Snapshot.CheckpointID != checkpoint || checkpoint == 0 {
			return nil, 0, fmt.Errorf("rescale snapshots have gaps, overlaps, or mixed checkpoint identities")
		}
		next = part.Groups.End
	}
	if next != assigned.End {
		return nil, 0, fmt.Errorf("rescale snapshots do not cover assigned range")
	}
	return ordered, checkpoint, nil
}
