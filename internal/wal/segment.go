package wal

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// SegmentInfo contains metadata about a segment.
type SegmentInfo struct {
	ID         int64     // Segment ID (starting offset)
	Path       string    // File path
	Size       int64     // Current size in bytes
	StartTime  time.Time // When the segment was created
	EndTime    time.Time // When the segment was closed (zero if still open)
	EntryCount int64     // Number of entries
	Corrupted  bool      // Whether corruption was detected
}

// Segment represents a single WAL segment file.
type Segment struct {
	id        int64  // Starting offset for this segment
	path      string // Full path to the segment file
	indexPath string // Full path to the index file

	file        *os.File
	writer      *bufio.Writer
	index       *SegmentIndex
	currentSize int64
	entryCount  int64

	mu        sync.RWMutex
	closed    bool
	corrupted bool
	startTime time.Time
	endTime   time.Time

	logger      zerolog.Logger
	metrics     *WALMetrics
	compression CompressionType
}

// OpenSegment opens an existing segment file.
func OpenSegment(id int64, dir string, logger zerolog.Logger, metrics *WALMetrics, compression CompressionType) (*Segment, error) {
	segmentPath := filepath.Join(dir, fmt.Sprintf("%020d%s", id, walFileExtension))
	indexPath := filepath.Join(dir, fmt.Sprintf("%020d%s", id, idxFileExtension))

	file, err := os.OpenFile(segmentPath, os.O_RDWR|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open segment file: %w", err)
	}

	// Get file info
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat segment file: %w", err)
	}

	// Open or create index
	index, err := OpenSegmentIndex(indexPath)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to open segment index: %w", err)
	}

	seg := &Segment{
		id:          id,
		path:        segmentPath,
		indexPath:   indexPath,
		file:        file,
		writer:      bufio.NewWriterSize(file, defaultSegmentWriterBufferSize),
		index:       index,
		currentSize: stat.Size(),
		startTime:   stat.ModTime(), // Approximate
		logger:      logger.With().Int64("segment_id", id).Logger(),
		metrics:     metrics,
		compression: compression,
	}

	// Scan the segment to build the index if needed
	if index.EntryCount() == 0 && stat.Size() > 0 {
		if err := seg.rebuildIndex(); err != nil {
			seg.Close()
			return nil, fmt.Errorf("failed to rebuild index: %w", err)
		}
	}

	seg.entryCount = index.EntryCount()
	if metrics != nil {
		metrics.UpdateSegmentSize(seg.currentSize)
	}

	return seg, nil
}

// CreateSegment creates a new segment file.
func CreateSegment(id int64, dir string, logger zerolog.Logger, metrics *WALMetrics, compression CompressionType) (*Segment, error) {
	segmentPath := filepath.Join(dir, fmt.Sprintf("%020d%s", id, walFileExtension))
	indexPath := filepath.Join(dir, fmt.Sprintf("%020d%s", id, idxFileExtension))

	// Create segment file
	file, err := os.OpenFile(segmentPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create segment file: %w", err)
	}

	// Create index
	index, err := CreateSegmentIndex(indexPath)
	if err != nil {
		file.Close()
		os.Remove(segmentPath)
		return nil, fmt.Errorf("failed to create segment index: %w", err)
	}

	seg := &Segment{
		id:          id,
		path:        segmentPath,
		indexPath:   indexPath,
		file:        file,
		writer:      bufio.NewWriterSize(file, defaultSegmentWriterBufferSize),
		index:       index,
		currentSize: 0,
		startTime:   time.Now(),
		logger:      logger.With().Int64("segment_id", id).Logger(),
		metrics:     metrics,
		compression: compression,
	}

	if metrics != nil {
		metrics.RecordSegmentCreated()
		metrics.UpdateSegmentSize(0)
	}

	return seg, nil
}

// Write writes an entry to the segment.
func (s *Segment) Write(entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("segment is closed")
	}

	if s.corrupted {
		return fmt.Errorf("segment is corrupted")
	}

	// Serialize the entry
	data, err := s.serializeEntry(entry)
	if err != nil {
		return fmt.Errorf("failed to serialize entry: %w", err)
	}

	// Write length header
	lengthBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuf, uint32(len(data)))

	if _, err := s.writer.Write(lengthBuf); err != nil {
		return fmt.Errorf("failed to write entry length: %w", err)
	}

	// Write entry data
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write entry data: %w", err)
	}

	// Update index
	fileOffset := s.currentSize
	if err := s.index.AddEntry(entry.Offset, fileOffset, int64(len(data)+4)); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	// Update metrics
	s.currentSize += int64(len(data) + 4)
	s.entryCount++
	if s.metrics != nil {
		s.metrics.UpdateSegmentSize(s.currentSize)
	}

	return nil
}

// Read reads an entry at the specified offset.
func (s *Segment) Read(offset int64) (*Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.corrupted {
		return nil, fmt.Errorf("segment is corrupted")
	}

	// Look up file offset in index
	fileOffset, size, err := s.index.GetEntry(offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get entry from index: %w", err)
	}

	// Read entry data
	data := make([]byte, size)
	if _, err := s.file.ReadAt(data, fileOffset); err != nil {
		return nil, fmt.Errorf("failed to read entry data: %w", err)
	}

	// Skip length header
	if len(data) < 4 {
		return nil, fmt.Errorf("invalid entry data: too short")
	}
	entryData := data[4:]

	// Deserialize entry
	entry, err := s.deserializeEntry(entryData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize entry: %w", err)
	}

	// Verify offset matches
	if entry.Offset != offset {
		return nil, fmt.Errorf("offset mismatch: expected %d, got %d", offset, entry.Offset)
	}

	return entry, nil
}

// Flush flushes the segment writer buffer.
func (s *Segment) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	return nil
}

// Sync syncs the segment file to disk.
func (s *Segment) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	// Flush writer first
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	// Sync file
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	// Sync index
	if err := s.index.Sync(); err != nil {
		return fmt.Errorf("failed to sync index: %w", err)
	}

	return nil
}

// Close closes the segment.
func (s *Segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	s.endTime = time.Now()

	// Flush and close writer
	if err := s.writer.Flush(); err != nil {
		s.logger.Error().Err(err).Msg("Failed to flush writer during close")
	}

	// Close index
	if err := s.index.Close(); err != nil {
		s.logger.Error().Err(err).Msg("Failed to close index")
	}

	// Close file
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	if s.metrics != nil {
		s.metrics.UpdateOpenFileDescriptors(-1)
	}

	return nil
}

// Delete deletes the segment files.
func (s *Segment) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		s.writer.Flush()
		s.index.Close()
		s.file.Close()
	}

	// Delete segment file
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete segment file: %w", err)
	}

	// Delete index file
	if err := os.Remove(s.indexPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete index file: %w", err)
	}

	if s.metrics != nil {
		s.metrics.RecordSegmentDeleted()
	}

	return nil
}

// Info returns segment information.
func (s *Segment) Info() SegmentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return SegmentInfo{
		ID:         s.id,
		Path:       s.path,
		Size:       s.currentSize,
		StartTime:  s.startTime,
		EndTime:    s.endTime,
		EntryCount: s.entryCount,
		Corrupted:  s.corrupted,
	}
}

// serializeEntry serializes an entry with compression.
func (s *Segment) serializeEntry(entry *Entry) ([]byte, error) {
	// Calculate CRC32 if not already set
	if entry.CRC32 == 0 {
		entry.CRC32 = s.calculateEntryCRC(entry)
	}

	// Serialize to binary format
	data, err := encodeEntry(entry)
	if err != nil {
		return nil, err
	}

	// Compress if needed
	if s.compression != CompressionNone {
		compressed, err := compress(s.compression, data)
		if err != nil {
			return nil, fmt.Errorf("failed to compress entry: %w", err)
		}
		if s.metrics != nil {
			s.metrics.RecordCompression(int64(len(data)), int64(len(compressed)), 0)
		}
		return compressed, nil
	}

	return data, nil
}

// deserializeEntry deserializes an entry with decompression.
func (s *Segment) deserializeEntry(data []byte) (*Entry, error) {
	// Decompress if needed
	if s.compression != CompressionNone {
		decompressed, err := decompress(s.compression, data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress entry: %w", err)
		}
		data = decompressed
	}

	// Deserialize from binary format
	entry, err := decodeEntry(data)
	if err != nil {
		return nil, err
	}

	// Verify CRC32
	expectedCRC := s.calculateEntryCRC(entry)
	if entry.CRC32 != expectedCRC {
		if s.metrics != nil {
			s.metrics.RecordCRCMismatch()
		}
		return nil, fmt.Errorf("CRC mismatch: expected %d, got %d", expectedCRC, entry.CRC32)
	}

	return entry, nil
}

// calculateEntryCRC calculates the CRC32 checksum for an entry.
func (s *Segment) calculateEntryCRC(entry *Entry) uint32 {
	h := crc32.NewIEEE()

	// Write timestamp
	binary.Write(h, binary.BigEndian, entry.Timestamp.UnixNano())

	// Write source ID
	h.Write([]byte(entry.SourceID))

	// Write partition ID
	binary.Write(h, binary.BigEndian, entry.PartitionID)

	// Write headers
	for k, v := range entry.Headers {
		h.Write([]byte(k))
		h.Write([]byte(v))
	}

	// Write key and value
	h.Write(entry.Key)
	h.Write(entry.Value)

	return h.Sum32()
}

// rebuildIndex rebuilds the index by scanning the segment file.
func (s *Segment) rebuildIndex() error {
	s.logger.Info().Msg("Rebuilding segment index")

	// Seek to beginning
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to beginning: %w", err)
	}

	reader := bufio.NewReader(s.file)
	fileOffset := int64(0)
	entryCount := int64(0)

	for {
		// Read length header
		lengthBuf := make([]byte, 4)
		if _, err := io.ReadFull(reader, lengthBuf); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read entry length: %w", err)
		}

		length := binary.BigEndian.Uint32(lengthBuf)
		if length == 0 || length > 10*1024*1024 { // Max 10MB per entry
			s.logger.Warn().Uint32("length", length).Msg("Invalid entry length detected")
			s.corrupted = true
			if s.metrics != nil {
				s.metrics.RecordCorruptedEntry()
			}
			break
		}

		// Read entry data
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			return fmt.Errorf("failed to read entry data: %w", err)
		}

		// Deserialize to get offset
		entry, err := s.deserializeEntry(data)
		if err != nil {
			s.logger.Warn().Err(err).Msg("Failed to deserialize entry during rebuild")
			s.corrupted = true
			if s.metrics != nil {
				s.metrics.RecordCorruptedEntry()
			}
			break
		}

		// Add to index
		if err := s.index.AddEntry(entry.Offset, fileOffset, int64(length+4)); err != nil {
			return fmt.Errorf("failed to add entry to index: %w", err)
		}

		fileOffset += int64(length + 4)
		entryCount++
	}

	// Seek back to end for appending
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("failed to seek to end: %w", err)
	}

	s.entryCount = entryCount
	s.logger.Info().Int64("entries", entryCount).Bool("corrupted", s.corrupted).Msg("Index rebuild complete")

	return nil
}
