package engine

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
)

var windowMetadataKey = []byte("window/meta")
var windowRecordsPrefix = []byte("window/records/")

// BindBackend binds an exclusively owned window backend before processing.
// Cached working state is bounded by MaxWindows and MaxStateBytes. Each record
// update, purge and metadata change is published atomically to the backend;
// portable checkpoints remain independent of the local backend directory.
func (p *WindowProcessor) BindBackend(backend StateBackend) error {
	store, ok := backend.(BatchedStateBackend)
	if !ok {
		return fmt.Errorf("window: backend requires atomic batches")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.backend != nil {
		return fmt.Errorf("window: backend already bound")
	}
	data, err := store.Get(windowMetadataKey)
	if errors.Is(err, ErrKeyNotFound) {
		p.backend = store
		if err = p.persist(p.watermark, p.stats, p.windows); err != nil {
			p.backend = nil
		}
		return err
	}
	if err != nil {
		return err
	}
	var snapshot windowSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("%w: window metadata: %v", ErrSnapshotCorrupt, err)
	}
	if len(snapshot.Windows) != 0 {
		return fmt.Errorf("%w: unexpected inline window records", ErrSnapshotCorrupt)
	}
	iterator := store.NewIterator(windowRecordsPrefix)
	defer iterator.Close()
	for iterator.Next() {
		var windows []retainedWindow
		if err = json.Unmarshal(iterator.Value(), &windows); err != nil {
			return fmt.Errorf("%w: window record: %v", ErrSnapshotCorrupt, err)
		}
		if len(windows) == 0 {
			return fmt.Errorf("%w: empty retained record", ErrSnapshotCorrupt)
		}
		for _, window := range windows {
			if string(iterator.Key()) != string(windowRecordsPrefix)+string(window.Key) {
				return fmt.Errorf("%w: window record key mismatch", ErrSnapshotCorrupt)
			}
		}
		snapshot.Windows = append(snapshot.Windows, windows...)
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	candidate, err := NewWindowProcessor(p.config, p.aggregator)
	if err != nil {
		return err
	}
	if err = candidate.Restore(binary.BigEndian.AppendUint32(body, crc32.ChecksumIEEE(body))); err != nil {
		return err
	}
	p.windows, p.watermark, p.stats = candidate.windows, candidate.watermark, candidate.stats
	p.backend = store
	return nil
}

// persist assumes p.mu is held. An empty list deletes a key's retained windows.
func (p *WindowProcessor) persist(watermark int64, stats WindowStats, updates map[string][]retainedWindow) error {
	if p.backend == nil {
		return nil
	}
	metadata, err := json.Marshal(windowSnapshot{Version: 1, Config: p.config, Watermark: watermark, Stats: stats})
	if err != nil {
		return err
	}
	changes := []StateMutation{{Key: windowMetadataKey, Value: metadata}}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		windows := updates[key]
		mutation := StateMutation{Key: append(append([]byte(nil), windowRecordsPrefix...), []byte(key)...), Delete: len(windows) == 0}
		if !mutation.Delete {
			mutation.Value, err = json.Marshal(windows)
			if err != nil {
				return err
			}
		}
		changes = append(changes, mutation)
	}
	return p.backend.ApplyBatch(changes)
}
