package sdk

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/keygroup"
)

// SetKeyGroupCount supplies the immutable job hash space before Open/restore.
func (op *ProcessOperator) SetKeyGroupCount(count int) { op.numKeyGroups = count }

// RestoreKeyGroupState preserves the SDK's existing state-key encoding. Value,
// list, map, TTL and timer entries are selected by their embedded user key;
// the operator-wide watermark is the minimum across contributing snapshots.
func (a *processAdapter) RestoreKeyGroupState(ctx context.Context, assigned keygroup.KeyGroupRange, parts []engine.KeyGroupSnapshot) error {
	if a.backend == nil {
		return engine.ErrBackendClosed
	}
	groups := a.numKeyGroups
	if groups == 0 {
		groups = keygroup.DefaultNumKeyGroups
	}
	if err := (keygroup.Config{NumKeyGroups: groups, Parallelism: 1}).Validate(); err != nil {
		return err
	}
	if len(parts) == 0 || assigned.Start >= assigned.End || int(assigned.End) > groups {
		return fmt.Errorf("sdk: invalid rescale ownership")
	}
	ordered := append([]engine.KeyGroupSnapshot(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Groups.Start < ordered[j].Groups.Start })
	next := assigned.Start
	checkpoint := ordered[0].Snapshot.CheckpointID
	kind := ordered[0].Snapshot.BackendType
	for _, part := range ordered {
		if part.Groups.Start != next || part.Groups.End <= next || part.Groups.End > assigned.End || checkpoint == 0 || part.Snapshot.CheckpointID != checkpoint || part.Snapshot.BackendType != kind {
			return fmt.Errorf("sdk: inconsistent rescale snapshots")
		}
		next = part.Groups.End
	}
	if next != assigned.End {
		return fmt.Errorf("sdk: incomplete rescale ownership")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "wire-process-rescale-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	target, err := engine.NewStateBackend(engine.StateBackendConfig{Type: kind, PebbleDataDir: filepath.Join(root, "merged")})
	if err != nil {
		return err
	}
	defer target.Close()
	minimum := int64(math.MaxInt64)
	for i, part := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := engine.NewStateBackend(engine.StateBackendConfig{Type: kind, PebbleDataDir: filepath.Join(root, fmt.Sprint(i))})
		if err != nil {
			return err
		}
		watermark, copyErr := copyProcessRange(ctx, source, target, part, groups)
		closeErr := source.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if watermark < minimum {
			minimum = watermark
		}
	}
	if err := target.Put(processWatermarkKey, binary.BigEndian.AppendUint64(nil, uint64(minimum))); err != nil {
		return err
	}
	snapshot, err := target.Checkpoint(checkpoint)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return a.RestoreState(snapshot)
}

func copyProcessRange(ctx context.Context, source, target engine.StateBackend, part engine.KeyGroupSnapshot, groups int) (int64, error) {
	if err := source.Restore(part.Snapshot); err != nil {
		return 0, err
	}
	watermark := int64(math.MinInt64)
	it := source.NewIterator(nil)
	defer it.Close()
	for it.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		key, value := it.Key(), it.Value()
		if bytes.Equal(key, processWatermarkKey) {
			if len(value) != 8 {
				return 0, fmt.Errorf("sdk: corrupt Process watermark")
			}
			watermark = int64(binary.BigEndian.Uint64(value))
			continue
		}
		userKey, err := processStateUserKey(key)
		if err != nil {
			return 0, err
		}
		if part.Groups.Contains(keygroup.KeyGroup(userKey, groups)) {
			if err := target.Put(key, value); err != nil {
				return 0, err
			}
		}
	}
	return watermark, nil
}

func processStateUserKey(key []byte) ([]byte, error) {
	if len(key) > 0 && key[0] == 'x' {
		key = key[1:]
	}
	if len(key) < 9 {
		return nil, fmt.Errorf("sdk: malformed managed state key")
	}
	if key[0] == 't' {
		return key[9:], nil
	}
	if key[0] != 'v' && key[0] != 'l' && key[0] != 'm' {
		return nil, fmt.Errorf("sdk: unknown managed state key kind")
	}
	size := binary.BigEndian.Uint64(key[1:9])
	if size > uint64(len(key)-9) {
		return nil, fmt.Errorf("sdk: malformed managed user key")
	}
	end := 9 + int(size)
	if len(key)-end < 8 {
		return nil, fmt.Errorf("sdk: missing state name")
	}
	nameSize := binary.BigEndian.Uint64(key[end : end+8])
	if nameSize > uint64(len(key)-end-8) || (key[0] != 'm' && nameSize != uint64(len(key)-end-8)) {
		return nil, fmt.Errorf("sdk: malformed state name")
	}
	return key[9:end], nil
}
