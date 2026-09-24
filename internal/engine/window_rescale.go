package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"

	"github.com/tarungka/wire/internal/keygroup"
)

// SetKeyGroupCount runs before Open and supplies the immutable job hash space.
func (op *EventTimeWindowOperator) SetKeyGroupCount(count int) { op.processor.numKeyGroups = count }

func (op *EventTimeWindowOperator) RestoreKeyGroupState(ctx context.Context, assigned keygroup.KeyGroupRange, parts []KeyGroupSnapshot) error {
	if op.backend == nil {
		return ErrBackendClosed
	}
	groups := op.processor.numKeyGroups
	if err := (keygroup.Config{NumKeyGroups: groups, Parallelism: 1}).Validate(); err != nil {
		return err
	}
	ordered, checkpoint, err := validateKeyGroupSnapshots(assigned, parts)
	if err != nil {
		return err
	}
	if int(assigned.End) > groups {
		return fmt.Errorf("window: rescale range exceeds hash space")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "wire-window-rescale-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	kind := ordered[0].Snapshot.BackendType
	merged := windowSnapshot{Version: 2, NumKeyGroups: groups, GroupWatermarks: make(map[uint16]int64), CheckpointID: checkpoint, Config: op.processor.config, Watermark: math.MaxInt64}
	for i, part := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if part.Snapshot.BackendType != kind {
			return fmt.Errorf("window: mixed backend formats")
		}
		source, err := NewStateBackend(StateBackendConfig{Type: kind, PebbleDataDir: filepath.Join(root, fmt.Sprint(i))})
		if err != nil {
			return err
		}
		candidate, err := NewWindowProcessor(op.processor.config, op.processor.aggregator)
		if err == nil {
			candidate.numKeyGroups = groups
			err = source.Restore(part.Snapshot)
		}
		if err == nil {
			err = candidate.BindBackend(source)
		}
		closeErr := source.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		for group := part.Groups.Start; group < part.Groups.End; group++ {
			watermark := candidate.watermark
			if floor, ok := candidate.groupWatermarks[group]; ok {
				watermark = max(watermark, floor)
			}
			merged.GroupWatermarks[group] = watermark
			merged.Watermark = min(merged.Watermark, watermark)
		}
		for key, windows := range candidate.windows {
			if part.Groups.Contains(keygroup.KeyGroup([]byte(key), groups)) {
				merged.Windows = append(merged.Windows, windows...)
			}
		}
	}
	// Operational counters are local to the new task. Retained state counts are
	// recomputed; historical source counters cannot be apportioned per key group.
	merged.Stats.RetainedWindows = len(merged.Windows)
	body, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	target, err := NewStateBackend(StateBackendConfig{Type: kind, PebbleDataDir: filepath.Join(root, "merged")})
	if err != nil {
		return err
	}
	defer target.Close()
	candidate, err := NewWindowProcessor(op.processor.config, op.processor.aggregator)
	if err != nil {
		return err
	}
	candidate.numKeyGroups = groups
	if err := candidate.BindBackend(target); err != nil {
		return err
	}
	if err := candidate.Restore(binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))); err != nil {
		return err
	}
	snapshot, err := target.Checkpoint(checkpoint)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return op.RestoreState(snapshot)
}
