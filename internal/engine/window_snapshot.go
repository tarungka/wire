package engine

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"
)

type windowSnapshot struct {
	Version      int
	CheckpointID uint64
	Config       WindowConfig
	Watermark    int64
	Windows      []retainedWindow
	Stats        WindowStats
}

// Checkpoint includes event-time progress and update flags, so recovery neither
// re-fires an unchanged result nor admits events for already-purged windows.
func (p *WindowProcessor) Checkpoint(checkpointID uint64) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := windowSnapshot{Version: 1, CheckpointID: checkpointID, Config: p.config, Watermark: p.watermark, Stats: p.stats}
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
	if snapshot.Version != 1 || snapshot.Config != p.config || len(snapshot.Windows) > p.config.MaxWindows || snapshot.Stats.RetainedWindows != len(snapshot.Windows) {
		return fmt.Errorf("%w: incompatible window snapshot", ErrSnapshotCorrupt)
	}
	windows := make(map[string][]retainedWindow)
	seen := make(map[string]map[[2]int64]bool)
	for _, w := range snapshot.Windows {
		if w.End <= w.Start || snapshot.Watermark >= p.deadline(w.End) || w.Fired && !w.Emitted || w.Fired != (snapshot.Watermark >= w.End) {
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
	if err := p.persist(snapshot.Watermark, snapshot.Stats, updates); err != nil {
		return err
	}
	p.windows = windows
	p.watermark = snapshot.Watermark
	p.stats = snapshot.Stats
	return nil
}
