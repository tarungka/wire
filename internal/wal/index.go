package wal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// IndexEntry represents a single entry in the segment index.
type IndexEntry struct {
	Offset     int64 // Logical offset
	FileOffset int64 // Physical offset in segment file
	Size       int64 // Size of the entry in bytes
}

// SegmentIndex manages the index for a segment.
type SegmentIndex struct {
	path    string
	file    *os.File
	entries []IndexEntry

	// Offset to entry mapping for fast lookups
	offsetMap map[int64]int // offset -> index in entries slice

	mu    sync.RWMutex
	dirty bool // Whether the index has unsaved changes
}

// OpenSegmentIndex opens an existing index file or creates it if it doesn't exist.
func OpenSegmentIndex(path string) (*SegmentIndex, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open index file: %w", err)
	}

	idx := &SegmentIndex{
		path:      path,
		file:      file,
		entries:   make([]IndexEntry, 0),
		offsetMap: make(map[int64]int),
	}

	// Load existing entries
	if err := idx.load(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to load index: %w", err)
	}

	return idx, nil
}

// CreateSegmentIndex creates a new index file.
func CreateSegmentIndex(path string) (*SegmentIndex, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create index file: %w", err)
	}

	return &SegmentIndex{
		path:      path,
		file:      file,
		entries:   make([]IndexEntry, 0),
		offsetMap: make(map[int64]int),
	}, nil
}

// AddEntry adds a new entry to the index.
func (idx *SegmentIndex) AddEntry(offset, fileOffset, size int64) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Check for duplicate offset
	if _, exists := idx.offsetMap[offset]; exists {
		return fmt.Errorf("offset %d already exists in index", offset)
	}

	entry := IndexEntry{
		Offset:     offset,
		FileOffset: fileOffset,
		Size:       size,
	}

	idx.entries = append(idx.entries, entry)
	idx.offsetMap[offset] = len(idx.entries) - 1
	idx.dirty = true

	return nil
}

// GetEntry retrieves the file offset and size for a given logical offset.
func (idx *SegmentIndex) GetEntry(offset int64) (fileOffset, size int64, err error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	index, exists := idx.offsetMap[offset]
	if !exists {
		return 0, 0, fmt.Errorf("offset %d not found in index", offset)
	}

	entry := idx.entries[index]
	return entry.FileOffset, entry.Size, nil
}

// GetEntries returns all entries in the index.
func (idx *SegmentIndex) GetEntries() []IndexEntry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Return a copy to prevent external modifications
	entries := make([]IndexEntry, len(idx.entries))
	copy(entries, idx.entries)
	return entries
}

// EntryCount returns the number of entries in the index.
func (idx *SegmentIndex) EntryCount() int64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return int64(len(idx.entries))
}

// GetOffsetRange returns the minimum and maximum offsets in the index.
func (idx *SegmentIndex) GetOffsetRange() (min, max int64, err error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.entries) == 0 {
		return 0, 0, fmt.Errorf("index is empty")
	}

	min = idx.entries[0].Offset
	max = idx.entries[len(idx.entries)-1].Offset

	// Verify ordering (indexes should be ordered by offset)
	for i := 1; i < len(idx.entries); i++ {
		if idx.entries[i].Offset < idx.entries[i-1].Offset {
			return 0, 0, fmt.Errorf("index is not properly ordered")
		}
		if idx.entries[i].Offset < min {
			min = idx.entries[i].Offset
		}
		if idx.entries[i].Offset > max {
			max = idx.entries[i].Offset
		}
	}

	return min, max, nil
}

// Sync persists the index to disk.
func (idx *SegmentIndex) Sync() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if !idx.dirty {
		return nil
	}

	// Truncate file
	if err := idx.file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate index file: %w", err)
	}

	// Seek to beginning
	if _, err := idx.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to beginning: %w", err)
	}

	// Write header (number of entries)
	if err := binary.Write(idx.file, binary.BigEndian, int64(len(idx.entries))); err != nil {
		return fmt.Errorf("failed to write entry count: %w", err)
	}

	// Write entries
	for _, entry := range idx.entries {
		if err := idx.writeEntry(entry); err != nil {
			return fmt.Errorf("failed to write entry: %w", err)
		}
	}

	// Sync to disk
	if err := idx.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync index file: %w", err)
	}

	idx.dirty = false
	return nil
}

// Close closes the index file.
func (idx *SegmentIndex) Close() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Sync if dirty
	if idx.dirty {
		if err := idx.sync(); err != nil {
			return fmt.Errorf("failed to sync before close: %w", err)
		}
	}

	return idx.file.Close()
}

// load reads the index from disk.
func (idx *SegmentIndex) load() error {
	// Seek to beginning
	if _, err := idx.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to beginning: %w", err)
	}

	// Check if file is empty
	stat, err := idx.file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat index file: %w", err)
	}
	if stat.Size() == 0 {
		return nil // Empty index
	}

	// Read header
	var count int64
	if err := binary.Read(idx.file, binary.BigEndian, &count); err != nil {
		return fmt.Errorf("failed to read entry count: %w", err)
	}

	// Sanity check
	if count < 0 || count > 10000000 { // Max 10M entries per segment
		return fmt.Errorf("invalid entry count: %d", count)
	}

	// Read entries
	idx.entries = make([]IndexEntry, 0, count)
	idx.offsetMap = make(map[int64]int, count)

	for i := int64(0); i < count; i++ {
		entry, err := idx.readEntry()
		if err != nil {
			return fmt.Errorf("failed to read entry %d: %w", i, err)
		}

		idx.entries = append(idx.entries, entry)
		idx.offsetMap[entry.Offset] = len(idx.entries) - 1
	}

	return nil
}

// sync is the internal sync method (called with lock held).
func (idx *SegmentIndex) sync() error {
	if !idx.dirty {
		return nil
	}

	// Truncate file
	if err := idx.file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate index file: %w", err)
	}

	// Seek to beginning
	if _, err := idx.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to beginning: %w", err)
	}

	// Write header
	if err := binary.Write(idx.file, binary.BigEndian, int64(len(idx.entries))); err != nil {
		return fmt.Errorf("failed to write entry count: %w", err)
	}

	// Write entries
	for _, entry := range idx.entries {
		if err := idx.writeEntry(entry); err != nil {
			return fmt.Errorf("failed to write entry: %w", err)
		}
	}

	// Sync to disk
	if err := idx.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync index file: %w", err)
	}

	idx.dirty = false
	return nil
}

// writeEntry writes a single index entry.
func (idx *SegmentIndex) writeEntry(entry IndexEntry) error {
	if err := binary.Write(idx.file, binary.BigEndian, entry.Offset); err != nil {
		return err
	}
	if err := binary.Write(idx.file, binary.BigEndian, entry.FileOffset); err != nil {
		return err
	}
	if err := binary.Write(idx.file, binary.BigEndian, entry.Size); err != nil {
		return err
	}
	return nil
}

// readEntry reads a single index entry.
func (idx *SegmentIndex) readEntry() (IndexEntry, error) {
	var entry IndexEntry
	if err := binary.Read(idx.file, binary.BigEndian, &entry.Offset); err != nil {
		return entry, err
	}
	if err := binary.Read(idx.file, binary.BigEndian, &entry.FileOffset); err != nil {
		return entry, err
	}
	if err := binary.Read(idx.file, binary.BigEndian, &entry.Size); err != nil {
		return entry, err
	}
	return entry, nil
}

// Truncate removes all entries after the given offset.
func (idx *SegmentIndex) Truncate(afterOffset int64) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Find the index to truncate at
	truncateAt := -1
	for i, entry := range idx.entries {
		if entry.Offset > afterOffset {
			truncateAt = i
			break
		}
	}

	if truncateAt == -1 {
		return nil // Nothing to truncate
	}

	// Remove entries from map
	for i := truncateAt; i < len(idx.entries); i++ {
		delete(idx.offsetMap, idx.entries[i].Offset)
	}

	// Truncate slice
	idx.entries = idx.entries[:truncateAt]
	idx.dirty = true

	return nil
}
