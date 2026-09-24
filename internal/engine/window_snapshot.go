package engine

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"

	"github.com/tarungka/wire/internal/keygroup"
)

type windowSnapshot struct {
	NumKeyGroups    int              `json:",omitempty"`
	GroupWatermarks map[uint16]int64 `json:",omitempty"`
	Version         int
	CheckpointID    uint64
	Config          WindowConfig
	Watermark       int64
	Windows         []retainedWindow
	Stats           WindowStats
}

// Checkpoint includes event-time progress and update flags, so recovery neither
// re-fires an unchanged result nor admits events for already-purged windows.
func (p *WindowProcessor) Checkpoint(checkpointID uint64) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := windowSnapshot{Version: windowSnapshotVersion(p.groupWatermarks), NumKeyGroups: p.numKeyGroups, GroupWatermarks: p.groupWatermarks, CheckpointID: checkpointID, Config: p.config, Watermark: p.watermark, Stats: p.stats}
	keys := make([]string, 0, len(p.windows))
	for key := range p.windows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshot.Windows = append(snapshot.Windows, p.windows[key]...)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return binary.BigEndian.AppendUint32(data, crc32.ChecksumIEEE(data)), nil
}

// Restore validates a complete snapshot before replacing live state. This is a
// local processor snapshot; durable checkpoint distribution belongs to runtime.
func (p *WindowProcessor) Restore(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(data) < 4 {
		return ErrSnapshotCorrupt
	}
	body := data[:len(data)-4]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(data[len(data)-4:]) {
		return ErrSnapshotCorrupt
	}
	var snapshot windowSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, err)
	}
	if snapshot.Config.MaxStateBytes == 0 {
		snapshot.Config.MaxStateBytes = 64 * 1024 * 1024
	}
	if (snapshot.Version != 1 && snapshot.Version != 2) || snapshot.Config != p.config || len(snapshot.Windows) > p.config.MaxWindows || snapshot.Stats.RetainedWindows != len(snapshot.Windows) {
		return fmt.Errorf("%w: incompatible window snapshot", ErrSnapshotCorrupt)
	}
	if snapshot.NumKeyGroups == 0 {
		snapshot.NumKeyGroups = p.numKeyGroups
	}
	if snapshot.NumKeyGroups != p.numKeyGroups || (snapshot.Version == 1 && len(snapshot.GroupWatermarks) > 0) {
		return fmt.Errorf("%w: incompatible window hash space", ErrSnapshotCorrupt)
	}
	for group := range snapshot.GroupWatermarks {
		if int(group) >= p.numKeyGroups {
			return fmt.Errorf("%w: invalid window watermark group", ErrSnapshotCorrupt)
		}
	}
	effective := func(key []byte) int64 {
		watermark := snapshot.Watermark
		if floor, ok := snapshot.GroupWatermarks[keygroup.KeyGroup(key, p.numKeyGroups)]; ok && floor > watermark {
			watermark = floor
		}
		return watermark
	}
	windows := make(map[string][]retainedWindow)
	seen := make(map[string]map[[2]int64]bool)
	for _, w := range snapshot.Windows {
		if w.End <= w.Start || effective(w.Key) >= p.deadline(w.End) || w.Fired && !w.Emitted || w.Fired != (effective(w.Key) >= w.End) {
			return fmt.Errorf("%w: invalid retained window", ErrSnapshotCorrupt)
		}
		key := string(w.Key)
		if seen[key] == nil {
			seen[key] = make(map[[2]int64]bool)
		}
		bounds := [2]int64{w.Start, w.End}
		if seen[key][bounds] {
			return fmt.Errorf("%w: duplicate retained window", ErrSnapshotCorrupt)
		}
		seen[key][bounds] = true

		if p.config.Kind != "session" {
			step := p.config.Size
			if p.config.Kind == "sliding" {
				step = p.config.Slide
			}
			end, err := windowEnd(w.Start, p.config.Size)
			if err != nil || w.Start%step != 0 || end != w.End {
				return fmt.Errorf("%w: invalid window bounds", ErrSnapshotCorrupt)
			}
		}
		windows[key] = append(windows[key], cloneWindow(w))
	}
	for key := range windows {
		sort.Slice(windows[key], func(i, j int) bool { return windows[key][i].Start < windows[key][j].Start })
		if p.config.Kind == "session" {
			for i := 1; i < len(windows[key]); i++ {
				if windows[key][i-1].End >= windows[key][i].Start {
					return fmt.Errorf("%w: unmerged session windows", ErrSnapshotCorrupt)
				}
			}
		}
	}
	snapshot.Stats.StateBytes = windowPayloadBytes(snapshot.Windows)
	if snapshot.Stats.StateBytes > p.config.MaxStateBytes {
		return fmt.Errorf("%w: state byte limit", ErrSnapshotCorrupt)
	}
	updates := make(map[string][]retainedWindow, len(windows)+len(p.windows))
	for key := range p.windows {
		updates[key] = nil
	}
	for key, value := range windows {
		updates[key] = value
	}
	if err := p.persistProgress(snapshot.Watermark, snapshot.Stats, updates, snapshot.GroupWatermarks); err != nil {
		return err
	}
	p.groupWatermarks = snapshot.GroupWatermarks
	p.windows = windows
	p.watermark = snapshot.Watermark
	p.stats = snapshot.Stats
	return nil
}

func windowSnapshotVersion(floors map[uint16]int64) int {
	if len(floors) > 0 {
		return 2
	}
	return 1
}
