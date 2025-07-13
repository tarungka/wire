package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rs/zerolog"
)

// IndexEntry represents an entry in the segment index.
type IndexEntry struct {
	Offset       int64
	FilePosition int64
	Size         int32
	Timestamp    int64 // Stored as UnixNano
}

// SegmentIndex provides an in-memory index for a segment.
type SegmentIndex struct {
	entries []IndexEntry
	mu      sync.RWMutex
	path    string
	logger  zerolog.Logger
}

// NewSegmentIndex creates a new segment index.
func NewSegmentIndex(path string, logger zerolog.Logger) *SegmentIndex {
	return &SegmentIndex{
		path:    path,
		logger:  logger.With().Str("sub_component", "index").Logger(),
		entries: make([]IndexEntry, 0),
	}
}

// Add adds a new entry to the index.
func (idx *SegmentIndex) Add(entry IndexEntry) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if len(idx.entries) > 0 && entry.Offset <= idx.entries[len(idx.entries)-1].Offset {
		idx.logger.Error().Int64("new_offset", entry.Offset).Int64("last_offset", idx.entries[len(idx.entries)-1].Offset).Msg("Index add: new offset is not greater than last offset")
		return fmt.Errorf("index add: new offset %d not > last offset %d", entry.Offset, idx.entries[len(idx.entries)-1].Offset)
	}

	idx.entries = append(idx.entries, entry)
	return nil
}

// Load loads the index from disk.
func (idx *SegmentIndex) Load() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	file, err := os.Open(idx.path)
	if err != nil {
		if os.IsNotExist(err) {
			idx.logger.Info().Msg("Index file does not exist, starting fresh")
			return nil
		}
		return fmt.Errorf("open index file: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		entry := IndexEntry{}
		err := binary.Read(reader, binary.BigEndian, &entry)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read index entry: %w", err)
		}
		idx.entries = append(idx.entries, entry)
	}

	idx.logger.Info().Int("entries", len(idx.entries)).Msg("Index loaded from disk")
	return nil
}

// Persist writes the index to disk.
func (idx *SegmentIndex) Persist() error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	file, err := os.Create(idx.path)
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, entry := range idx.entries {
		err := binary.Write(writer, binary.BigEndian, entry)
		if err != nil {
			return fmt.Errorf("write index entry: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush index writer: %w", err)
	}

	idx.logger.Info().Int("entries", len(idx.entries)).Msg("Index persisted to disk")
	return nil
}

// Count returns the number of entries in the index.
func (idx *SegmentIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.entries)
}
