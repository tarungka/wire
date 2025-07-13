package wal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

var (
	// ErrWALClosed is returned when an operation is attempted on a closed WAL.
	ErrWALClosed = errors.New("wal: log is closed")
	// ErrWALCorrupted is returned when a corruption is detected in a WAL segment.
	ErrWALCorrupted = errors.New("wal: log corruption detected")
	// ErrSegmentNotFound is returned when a segment for a given offset is not found.
	ErrSegmentNotFound = errors.New("wal: segment not found")
	// ErrEntryNotFound is returned when an entry at a specific offset is not found.
	ErrEntryNotFound = errors.New("wal: entry not found")
)

const (
	// Default buffer size for segment writers
	defaultSegmentWriterBufferSize = 64 * 1024 // 64KB

	// File extension for WAL segment files
	walFileExtension = ".wal"
	// File extension for WAL index files
	idxFileExtension = ".idx"

	// Prefix for corrupted segment files
	corruptedFileSuffix = ".corrupted"

	// Length of the CRC32 checksum in bytes
	crc32ChecksumLength = 4
	// Length of the entry length header in bytes
	entryLengthHeaderSize = 4 // int32 for entry length
)

// Entry represents a single WAL entry.
type Entry struct {
	// Offset is the logical offset of the entry in the WAL.
	Offset int64
	// Timestamp is the time the entry was written or an event time associated with the data.
	Timestamp time.Time

	// SourceID identifies the source of the data.
	SourceID string
	// PartitionID identifies the partition or shard ID from the source.
	PartitionID int32
	// Headers contains custom metadata for the entry.
	Headers map[string]string

	// Key is an optional key for the entry, e.g., from Kafka.
	Key []byte
	// Value is the actual data payload.
	Value []byte

	// CRC32 is the checksum for the entry data (excluding the CRC itself) for corruption detection.
	// The CRC is calculated over: Timestamp, SourceID, PartitionID, Headers, Key, Value.
	// The Offset is part of the segment's internal structure or index, not directly part of the CRC'd data here.
	CRC32 uint32
}

// WAL represents a Write-Ahead Log for durable data storage.
type WAL struct {
	config WALConfig
	logger zerolog.Logger

	dir            string
	maxSegmentSize int64 // From config.SegmentSize
	maxSegments    int   // From config.MaxSegments

	currentSegment *Segment       // Active segment for writing
	segments       []*SegmentInfo // List of all known segments (metadata)

	mu          sync.RWMutex // Protects segments list and currentSegment pointer during rotation
	writeMu     sync.Mutex   // Serializes writes to the current segment file and offset increments
	writeOffset int64        // Logical offset for the next entry to be written

	// Lifecycle
	closed     int32          // Atomic boolean; 1 if closed, 0 otherwise
	shutdownCh chan struct{}  // Signals background goroutines to stop
	syncLoopWg sync.WaitGroup // Waits for syncLoop to finish

	// Compression
	compressionType CompressionType // Parsed from config.Compression

	// Metrics (TODO: Implement WALMetrics struct and integration)
	metrics *WALMetrics
}

// SegmentInfo holds metadata about a WAL segment.
// This is kept in memory for quick access and management.
type SegmentInfo struct {
	ID          int64 // Segment ID (typically a timestamp)
	Path        string
	IndexPath   string
	StartOffset int64 // Logical offset of the first entry in this segment
	EndOffset   int64 // Logical offset of the last entry in this segment (inclusive)
	Created     time.Time
	EndTime     time.Time // When the segment was closed
	Size        int64     // Physical size on disk
	EntryCount  int64     // Number of entries in the segment
	IsActive    bool      // True if this is the current segment for writes
	Corrupted   bool      // True if corruption was detected
}

// NewWAL creates a new Write-Ahead Log instance.
// It recovers state from existing segment files in the directory if any.
func NewWAL(config WALConfig, logger zerolog.Logger) (*WAL, error) {
	if config.Directory == "" {
		return nil, errors.New("wal: directory cannot be empty")
	}
	if config.SegmentSize <= 0 {
		config.SegmentSize = DefaultWALConfig().SegmentSize
		logger.Info().Msgf("WAL SegmentSize not specified or invalid, using default: %d bytes", config.SegmentSize)
	}
	if config.MaxSegments <= 0 {
		config.MaxSegments = DefaultWALConfig().MaxSegments
		logger.Info().Msgf("WAL MaxSegments not specified or invalid, using default: %d", config.MaxSegments)
	}
	if config.SyncInterval < 0 { // 0 is allowed (sync on write if FlushOnWrite is true)
		config.SyncInterval = DefaultWALConfig().SyncInterval
		logger.Info().Msgf("WAL SyncInterval invalid, using default: %s", config.SyncInterval)
	}

	// Ensure WAL directory exists
	if err := os.MkdirAll(config.Directory, 0755); err != nil {
		return nil, fmt.Errorf("wal: create directory %s: %w", config.Directory, err)
	}

	compressionType, err := ParseCompressionType(config.Compression)
	if err != nil {
		logger.Warn().Err(err).Str("compression_type", config.Compression).Msg("WAL unsupported compression type, defaulting to none")
		compressionType = CompressionNone
		config.Compression = "none" // Update config to reflect actual type
	}

	wal := &WAL{
		config:          config,
		logger:          logger.With().Str("component", "wal").Logger(),
		dir:             config.Directory,
		maxSegmentSize:  config.SegmentSize,
		maxSegments:     config.MaxSegments,
		segments:        make([]*SegmentInfo, 0),
		shutdownCh:      make(chan struct{}),
		compressionType: compressionType,
		metrics:         NewWALMetrics(),
	}

	// Recover existing segments and state
	if err := wal.recover(); err != nil {
		return nil, fmt.Errorf("wal: recovery failed: %w", err)
	}

	// If no segments were recovered (e.g. new WAL) or last segment is full, create a new one.
	// The recover() function should ideally open the last segment for append or create a new one if needed.
	// Let's ensure currentSegment is set.
	if wal.currentSegment == nil {
		if err := wal.createNewSegment(); err != nil {
			return nil, fmt.Errorf("wal: failed to create initial segment: %w", err)
		}
	}

	// Start background sync loop
	if wal.config.SyncInterval > 0 && !wal.config.FlushOnWrite { // Only if not syncing on every write
		wal.syncLoopWg.Add(1)
		go wal.syncLoop()
	}

	wal.logger.Info().
		Str("directory", wal.dir).
		Int64("segment_size", wal.maxSegmentSize).
		Int("max_segments", wal.maxSegments).
		Dur("sync_interval", wal.config.SyncInterval).
		Str("compression", wal.config.Compression).
		Bool("flush_on_write", wal.config.FlushOnWrite).
		Int64("recovered_offset", wal.writeOffset).
		Msg("WAL initialized")

	return wal, nil
}

// Append writes an entry to the WAL.
// It returns the logical offset of the written entry or an error.
func (w *WAL) Append(entry *Entry) (int64, error) {
	if atomic.LoadInt32(&w.closed) == 1 {
		return 0, ErrWALClosed
	}

	startTime := time.Now()

	// Prepare entry data
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	// Check segment rotation
	if w.currentSegment == nil || w.currentSegment.currentSize >= w.maxSegmentSize {
		var currentSize int64
		if w.currentSegment != nil {
			currentSize = w.currentSegment.currentSize
		}
		w.logger.Info().Msgf("Current segment full or nil (size: %d, max: %d). Rotating.", currentSize, w.maxSegmentSize)
		if err := w.createNewSegment(); err != nil {
			if w.metrics != nil {
				w.metrics.RecordWrite(0, time.Since(startTime), false)
			}
			return 0, fmt.Errorf("wal: create new segment: %w", err)
		}
	}

	currentOffset := atomic.LoadInt64(&w.writeOffset)
	entry.Offset = currentOffset

	// Write the entry to the segment (segment handles serialization, compression, and indexing)
	if err := w.currentSegment.Write(entry); err != nil {
		w.logger.Error().Err(err).Msg("Failed to write entry to segment")
		if w.metrics != nil {
			w.metrics.RecordWrite(0, time.Since(startTime), false)
		}
		return 0, fmt.Errorf("wal: write entry to segment: %w", err)
	}

	// Get the actual size written for metrics
	info := w.currentSegment.Info()
	bytesWritten := info.Size - w.currentSegment.currentSize + int64(entryLengthHeaderSize+len(entry.Value))

	atomic.AddInt64(&w.writeOffset, 1) // Increment logical offset for next entry

	if w.config.FlushOnWrite {
		if err := w.currentSegment.Flush(); err != nil {
			if w.metrics != nil {
				w.metrics.RecordWrite(0, time.Since(startTime), false)
			}
			return 0, fmt.Errorf("wal: flush segment: %w", err)
		}
		// SyncInterval=0 with FlushOnWrite means sync on every write
		if w.config.SyncInterval == 0 {
			syncStart := time.Now()
			if err := w.currentSegment.Sync(); err != nil {
				if w.metrics != nil {
					w.metrics.RecordSync(time.Since(syncStart), false)
					w.metrics.RecordWrite(0, time.Since(startTime), false)
				}
				return 0, fmt.Errorf("wal: sync segment: %w", err)
			}
			if w.metrics != nil {
				w.metrics.RecordSync(time.Since(syncStart), true)
			}
		}
	}

	latency := time.Since(startTime)
	if w.metrics != nil {
		w.metrics.RecordWrite(bytesWritten, latency, true)
	}
	return currentOffset, nil
}

// recover scans segment files in the WAL directory to rebuild state.
func (w *WAL) recover() error {
	startTime := time.Now()
	// Recovery metrics tracked at the end

	w.logger.Info().Msg("Starting WAL recovery process...")
	files, err := filepath.Glob(filepath.Join(w.dir, "*"+walFileExtension))
	if err != nil {
		return fmt.Errorf("list segments: %w", err)
	}
	if len(files) == 0 {
		w.logger.Info().Msg("No existing WAL segments found. Initializing new WAL state.")
		// writeOffset remains 0, currentSegment will be created by NewWAL if nil
		if w.metrics != nil {
			w.metrics.RecordRecovery(0, time.Since(startTime), true)
		}
		return nil
	}

	sort.Strings(files) // Sorts by filename, which includes timestamp-based ID

	var recoveredSegments []*SegmentInfo
	var segmentsScannedDuringRecovery uint64
	var entriesRecoveredDuringRecovery uint64
	var corruptedSegmentsDuringRecovery uint64
	var maxOffsetSeen int64 = -1 // Start before the first possible offset

	for _, segPath := range files {
		segmentID, err := parseSegmentID(segPath)
		if err != nil {
			w.logger.Error().Err(err).Str("path", segPath).Msg("Failed to parse segment ID during recovery, skipping file.")
			// Potentially move this file to a .badformat suffix or similar
			continue
		}
		segmentsScannedDuringRecovery++
		w.logger.Info().Str("segment_path", segPath).Int64("segment_id", segmentID).Msg("Recovering segment")

		// Attempt to open the segment
		segment, err := OpenSegment(segmentID, w.dir, w.logger, w.metrics, w.compressionType)
		if err != nil {
			w.logger.Error().Err(err).Str("path", segPath).Msg("Failed to open segment for recovery, attempting to mark as corrupted.")
			idxPath := filepath.Join(w.dir, fmt.Sprintf("%020d%s", segmentID, idxFileExtension))
			w.handleCorruptedSegmentFile(segPath, idxPath)
			corruptedSegmentsDuringRecovery++
			continue
		}

		// Get segment info
		info := segment.Info()

		// Check if segment is corrupted
		if info.Corrupted {
			w.logger.Warn().Int64("segment_id", segment.id).Msg("Segment is corrupted. Marking as corrupted.")
			segment.Close()
			corruptedSegmentsDuringRecovery++
			w.handleCorruptedSegmentFile(info.Path, info.Path+".idx")
			continue
		}

		// Get the entry count
		entriesInSegment := info.EntryCount
		if entriesInSegment > 0 {
			entriesRecoveredDuringRecovery += uint64(entriesInSegment)
			// Get offset range from index
			if minOffset, maxOffset, err := segment.index.GetOffsetRange(); err == nil {
				if maxOffset > maxOffsetSeen {
					maxOffsetSeen = maxOffset
				}
			}
		}

		segInfo := &SegmentInfo{
			ID:          info.ID,
			Path:        info.Path,
			IndexPath:   info.Path + ".idx",
			StartOffset: -1, // Will be set based on index
			EndOffset:   -1, // Will be set based on index
			Created:     info.StartTime,
			EndTime:     info.EndTime,
			Size:        info.Size,
			EntryCount:  info.EntryCount,
			IsActive:    false,
			Corrupted:   info.Corrupted,
		}

		// Get offset range if segment has entries
		if entriesInSegment > 0 {
			if minOffset, maxOffset, err := segment.index.GetOffsetRange(); err == nil {
				segInfo.StartOffset = minOffset
				segInfo.EndOffset = maxOffset
			}
		}

		recoveredSegments = append(recoveredSegments, segInfo)

		// If this segment had entries, sync the index
		if info.EntryCount > 0 {
			if err := segment.index.Sync(); err != nil {
				w.logger.Error().Err(err).Int64("segment_id", segment.id).Msg("Failed to sync index during recovery")
				// This is not ideal, but we might proceed if data is readable.
			}
		}

		// Close the segment as we are done scanning it for now.
		// The last segment will be reopened for writes later.
		if err := segment.Close(); err != nil {
			w.logger.Error().Err(err).Int64("segment_id", segment.id).Msg("Error closing segment during recovery scan.")
		}
	}

	w.segments = recoveredSegments
	w.writeOffset = maxOffsetSeen + 1 // Next offset to write

	// Open the last segment for writing, or create a new one
	if len(w.segments) > 0 {
		lastSegInfo := w.segments[len(w.segments)-1]
		w.logger.Info().Str("segment_id", segmentIDToString(lastSegInfo.ID)).Msg("Opening last recovered segment for append.")
		// OpenSegment should handle creating the file object appropriately for append.
		// We need to ensure it's the *same* Segment instance potentially.
		// For simplicity, let's re-open.
		lastSegment, err := OpenSegment(lastSegInfo.ID, w.dir, w.logger, w.metrics, w.compressionType)
		if err != nil {
			w.logger.Error().Err(err).Msg("Failed to re-open last segment for writing after recovery. Creating new segment.")
			// Fall through to createNewSegment
		} else {
			// The segment is already properly opened for append by OpenSegment
			w.currentSegment = lastSegment
			lastSegInfo.IsActive = true
		}
	}

	if w.currentSegment == nil {
		w.logger.Info().Msg("No valid segments to append to after recovery, or last segment opening failed. Creating a new segment.")
		if err := w.createNewSegment(); err != nil {
			if w.metrics != nil {
				w.metrics.RecordRecovery(0, time.Since(startTime), false)
			}
			return fmt.Errorf("create new segment after recovery: %w", err)
		}
	}

	if w.metrics != nil {
		w.metrics.RecordRecovery(int64(entriesRecoveredDuringRecovery), time.Since(startTime), true)
		if corruptedSegmentsDuringRecovery > 0 {
			for i := uint64(0); i < corruptedSegmentsDuringRecovery; i++ {
				w.metrics.RecordCorruptedEntry()
			}
		}
	}

	w.logger.Info().
		Int64("recovered_offset", w.writeOffset-1). // -1 because writeOffset is the *next* offset
		Int("segments_loaded", len(w.segments)).
		Msg("WAL recovery completed.")
	return nil
}

func (w *WAL) handleCorruptedSegmentFile(segmentPath, indexPath string) {
	w.logger.Warn().Str("path", segmentPath).Msg("Handling corrupted segment file.")
	corruptedSegPath := segmentPath + corruptedFileSuffix
	// Metric for corrupted segments is incremented in recover()
	if err := os.Rename(segmentPath, corruptedSegPath); err != nil {
		w.logger.Error().Err(err).Str("original_path", segmentPath).Str("new_path", corruptedSegPath).Msg("Failed to rename corrupted segment file.")
	} else {
		w.logger.Info().Str("original_path", segmentPath).Str("new_path", corruptedSegPath).Msg("Renamed corrupted segment file.")
	}
	// Also attempt to rename index if it exists
	if _, err := os.Stat(indexPath); err == nil {
		corruptedIdxPath := indexPath + corruptedFileSuffix
		if err := os.Rename(indexPath, corruptedIdxPath); err != nil {
			w.logger.Error().Err(err).Str("original_path", indexPath).Str("new_path", corruptedIdxPath).Msg("Failed to rename corrupted index file.")
		} else {
			w.logger.Info().Str("original_path", indexPath).Str("new_path", corruptedIdxPath).Msg("Renamed corrupted index file.")
		}
	}
}

// syncLoop periodically flushes and syncs the current segment to disk.
func (w *WAL) syncLoop() {
	defer w.syncLoopWg.Done()
	ticker := time.NewTicker(w.config.SyncInterval)
	defer ticker.Stop()

	w.logger.Info().Dur("interval", w.config.SyncInterval).Msg("WAL sync loop started.")
	for {
		select {
		case <-w.shutdownCh:
			w.logger.Info().Msg("WAL sync loop shutting down.")
			// Final sync before exiting
			w.mu.RLock() // Read lock to access currentSegment safely
			if w.currentSegment != nil {
				w.currentSegment.Flush()
				w.currentSegment.Sync()
			}
			w.mu.RUnlock()
			return
		case <-ticker.C:
			w.mu.RLock() // Read lock to access currentSegment safely
			if w.currentSegment != nil && atomic.LoadInt32(&w.closed) == 0 {
				// w.logger.Debug().Msg("Sync loop: flushing and syncing current segment.")
				syncStart := time.Now()
				if err := w.currentSegment.Flush(); err != nil {
					w.logger.Error().Err(err).Int64("segment", w.currentSegment.id).Msg("Sync loop: failed to flush segment")
				}
				if err := w.currentSegment.Sync(); err != nil {
					if w.metrics != nil {
						w.metrics.RecordSync(time.Since(syncStart), false)
					}
					w.logger.Error().Err(err).Int64("segment", w.currentSegment.id).Msg("Sync loop: failed to sync segment")
				} else {
					if w.metrics != nil {
						w.metrics.RecordSync(time.Since(syncStart), true)
					}
				}
				// If sync was successful, it's implied. No specific success metric here unless needed.
			}
			w.mu.RUnlock()
		}
	}
}

// Close flushes any buffered data, syncs to disk, and closes the WAL.
func (w *WAL) Close() error {
	if !atomic.CompareAndSwapInt32(&w.closed, 0, 1) {
		return ErrWALClosed // Already closed or closing
	}

	w.logger.Info().Msg("Closing WAL...")

	// Signal and wait for syncLoop to stop
	if w.config.SyncInterval > 0 && !w.config.FlushOnWrite {
		close(w.shutdownCh)
		w.syncLoopWg.Wait()
	}

	w.mu.Lock() // Exclusive lock for final operations
	defer w.mu.Unlock()

	var lastErr error
	if w.currentSegment != nil {
		w.logger.Info().Int64("segment_id", w.currentSegment.id).Msg("Closing current segment.")
		if err := w.currentSegment.Close(); err != nil {
			w.logger.Error().Err(err).Int64("segment_id", w.currentSegment.id).Msg("Error closing current segment")
			lastErr = err
		}
		w.currentSegment = nil
	}

	// It might be useful to persist all segment infos one last time if they changed.
	// For now, segment info is mostly built during recovery.

	w.logger.Info().Msg("WAL closed.")
	return lastErr
}

// Purge removes all WAL segment files and their indexes from the directory.
// This is a destructive operation and should be used with caution (e.g., for testing).
func (w *WAL) Purge() error {
	if atomic.LoadInt32(&w.closed) == 0 {
		return errors.New("wal: cannot purge open WAL, close it first")
	}

	w.mu.Lock() // Ensure no other operations if called concurrently (though Close should prevent this)
	defer w.mu.Unlock()

	w.logger.Warn().Str("directory", w.dir).Msg("Purging all WAL files.")

	// Remove all .wal and .idx files
	walPattern := filepath.Join(w.dir, "*"+walFileExtension)
	idxPattern := filepath.Join(w.dir, "*"+idxFileExtension)
	corruptedPattern := filepath.Join(w.dir, "*"+corruptedFileSuffix)

	filesToRemove, _ := filepath.Glob(walPattern)
	idxFiles, _ := filepath.Glob(idxPattern)
	filesToRemove = append(filesToRemove, idxFiles...)
	corruptedFiles, _ := filepath.Glob(corruptedPattern)
	filesToRemove = append(filesToRemove, corruptedFiles...)

	var firstErr error
	for _, f := range filesToRemove {
		if err := os.Remove(f); err != nil {
			w.logger.Error().Err(err).Str("file", f).Msg("Failed to remove file during purge.")
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	// Reset in-memory state
	w.segments = make([]*SegmentInfo, 0)
	w.currentSegment = nil
	w.writeOffset = 0 // Reset logical offset

	return firstErr
}

// Helper to get segment path
func segmentPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d%s", id, walFileExtension))
}

// Helper to get segment index path
func segmentIndexPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d%s", id, idxFileExtension))
}

// Helper to parse segment ID from path
func parseSegmentID(path string) (int64, error) {
	fileName := filepath.Base(path)
	// Expected format: 00000000000000000001.wal
	var id int64
	// Remove suffix first
	nameWithoutExt := strings.TrimSuffix(fileName, walFileExtension)
	nameWithoutExt = strings.TrimSuffix(nameWithoutExt, corruptedFileSuffix) // In case it's a corrupted file name

	// Parse the ID part
	id, err := strconv.ParseInt(nameWithoutExt, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid segment file name format: %s (error: %v)", fileName, err)
	}
	return id, nil
}

func segmentIDToString(id int64) string {
	return fmt.Sprintf("%020d", id)
}

// --- These will be moved to segment.go or similar ---
// func (w *WAL) createNewSegment() error { /* ... */ }
// func (w *WAL) deleteOldestSegment() error { /* ... */ }
// --- Compression needs its own file or section ---
// func (w *WAL) compress(data []byte) ([]byte, error) { /* ... */ }
// func (w *WAL) decompress(data []byte) ([]byte, error) { /* ... */ }

// Ensure WALConfig is accessible.
// CompressionType, ParseCompressionType, compress, decompress are now in compression.go.
// Segment, SegmentIndex, IndexEntry, and their methods are in segment.go and index.go.

// createNewSegment creates a new segment for writing.
func (w *WAL) createNewSegment() error {
	w.logger.Info().Msg("Creating new WAL segment")

	w.mu.Lock()
	defer w.mu.Unlock()

	// Close current segment if it exists
	if w.currentSegment != nil {
		w.logger.Info().Int64("segment_id", w.currentSegment.id).Msg("Closing current segment before creating new one")

		// Sync before closing
		if err := w.currentSegment.Sync(); err != nil {
			w.logger.Error().Err(err).Msg("Failed to sync segment before closing")
		}

		if err := w.currentSegment.Close(); err != nil {
			w.logger.Error().Err(err).Msg("Error closing current segment")
		}

		// Update SegmentInfo for the old segment
		for _, si := range w.segments {
			if si.ID == w.currentSegment.id {
				si.IsActive = false
				si.EndOffset = atomic.LoadInt64(&w.writeOffset) - 1
				info := w.currentSegment.Info()
				si.Size = info.Size
				si.EndTime = info.EndTime
				si.EntryCount = info.EntryCount
				break
			}
		}

		if w.metrics != nil {
			w.metrics.RecordSegmentRotated()
		}
	}

	// Create new segment
	newSegmentID := time.Now().UnixNano()
	newSeg, err := CreateSegment(newSegmentID, w.dir, w.logger, w.metrics, w.compressionType)
	if err != nil {
		return fmt.Errorf("failed to create new segment: %w", err)
	}

	w.currentSegment = newSeg
	w.logger.Info().Int64("segment_id", newSegmentID).Msg("Created new segment")

	// Add to segments list
	segInfo := &SegmentInfo{
		ID:          newSegmentID,
		Path:        filepath.Join(w.dir, fmt.Sprintf("%020d%s", newSegmentID, walFileExtension)),
		IndexPath:   filepath.Join(w.dir, fmt.Sprintf("%020d%s", newSegmentID, idxFileExtension)),
		StartOffset: atomic.LoadInt64(&w.writeOffset),
		EndOffset:   -1, // Will be set when segment is closed
		Created:     time.Now(),
		Size:        0,
		IsActive:    true,
		EntryCount:  0,
		Corrupted:   false,
	}
	w.segments = append(w.segments, segInfo)

	// Enforce retention policy
	if len(w.segments) > w.maxSegments {
		go w.deleteOldestSegment()
	}

	return nil
}

func (w *WAL) deleteOldestSegment() {
	w.mu.Lock() // Lock for modifying w.segments

	if len(w.segments) <= w.maxSegments {
		w.mu.Unlock()
		return
	}

	// Sort segments by ID (which is timestamp) to be sure, though append order should maintain this.
	// SegmentInfo slice needs to be sortable.
	sort.Slice(w.segments, func(i, j int) bool {
		return w.segments[i].ID < w.segments[j].ID
	})

	segToDeleteInfo := w.segments[0]
	if segToDeleteInfo.IsActive { // Should not happen if logic is correct
		w.logger.Error().Str("segment_id", segmentIDToString(segToDeleteInfo.ID)).Msg("Attempted to delete active segment. This is a bug.")
		w.mu.Unlock()
		return
	}
	w.mu.Unlock() // Unlock before performing file operations

	w.logger.Info().Str("segment_id", segmentIDToString(segToDeleteInfo.ID)).Msg("Deleting oldest segment due to retention policy.")
	if err := os.Remove(segToDeleteInfo.Path); err != nil {
		w.logger.Error().Err(err).Str("path", segToDeleteInfo.Path).Msg("Failed to delete old segment file.")
	}
	if err := os.Remove(segToDeleteInfo.IndexPath); err != nil {
		// Log if index file exists and fails to delete, but don't make it a critical error if segment is gone.
		if !os.IsNotExist(err) {
			w.logger.Error().Err(err).Str("path", segToDeleteInfo.IndexPath).Msg("Failed to delete old segment index file.")
		}
	}

	// Update metrics
	if w.metrics != nil {
		w.metrics.RecordSegmentDeleted()
	}

	w.mu.Lock() // Lock again to modify slice
	// Remove from the slice
	w.segments = w.segments[1:]
	w.mu.Unlock()
}
