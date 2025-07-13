package wal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

// Segment represents a single segment of the WAL.
// It contains a file handle, a buffered writer, and an index.
type Segment struct {
	wal          *WAL // Reference back to WAL for config, logger
	id           int64
	idStr        string // string version of ID for logging
	path         string
	indexPath    string
	file         *os.File
	writer       *bufio.Writer // Buffered writer for efficiency
	index        *SegmentIndex
	size         int64 // Current physical size of the segment file on disk
	startOffset  int64 // Logical offset of the first entry
	endOffset    int64 // Logical offset of the last entry (inclusive)
	maxSizeBytes int64 // Max size for this segment (from WAL config)
	created      time.Time
	logger       zerolog.Logger
}

// OpenSegment opens an existing segment file or creates a new one.
// It also loads the corresponding index file.
func OpenSegment(dir string, id int64, bufferSize int, logger zerolog.Logger) (*Segment, error) {
	segPath := segmentPath(dir, id)
	idxPath := segmentIndexPath(dir, id)

	file, err := os.OpenFile(segPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open segment file %s: %w", segPath, err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat segment file %s: %w", segPath, err)
	}

	s := &Segment{
		id:           id,
		idStr:        segmentIDToString(id),
		path:         segPath,
		indexPath:    idxPath,
		file:         file,
		writer:       bufio.NewWriterSize(file, bufferSize),
		index:        NewSegmentIndex(idxPath, logger),
		size:         stat.Size(),
		created:      stat.ModTime(),
		logger:       logger.With().Str("sub_component", "segment").Str("segment_id", segmentIDToString(id)).Logger(),
		startOffset:  -1,
		endOffset:    -1,
	}

	if err := s.index.Load(); err != nil {
		s.logger.Warn().Err(err).Msg("Failed to load index, it may be rebuilt on recovery")
	}

	return s, nil
}

// Close closes the segment file and its index.
func (s *Segment) Close() error {
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			s.logger.Error().Err(err).Msg("Failed to flush segment writer on close")
		}
	}

	if s.file != nil {
		err := s.file.Close()
		s.file = nil
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to close segment file")
			return err
		}
	}

	if s.index != nil {
		if s.index.Count() > 0 {
			if err := s.index.Persist(); err != nil {
				s.logger.Error().Err(err).Msg("Failed to persist segment index on close")
			}
		}
	}

	s.logger.Info().Str("segment_id", s.idStr).Msg("Segment closed.")
	return nil
}

// Flush flushes the segment's buffered writer.
func (s *Segment) Flush() error {
	if s.writer == nil {
		return errors.New("segment writer is nil")
	}
	return s.writer.Flush()
}

// Sync flushes the segment's file to disk.
func (s *Segment) Sync() error {
	if s.file == nil {
		return errors.New("segment file is nil")
	}
	return s.file.Sync()
}

// ScanAndVerify scans a segment file to verify its integrity and rebuild the index if necessary.
func (s *Segment) ScanAndVerify(expectedStartOffset int64) (lastOffset int64, valid bool) {
	s.logger.Info().Msg("Scanning and verifying segment")
	file, err := os.Open(s.path)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to open segment file for verification")
		return -1, false
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var currentPos int64
	var offset int64 = expectedStartOffset

	for {
		lenBuf := make([]byte, entryLengthHeaderSize)
		_, err := io.ReadFull(reader, lenBuf)
		if err == io.EOF {
			break // End of segment
		}
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to read entry length")
			return -1, false
		}

		entryLen := binary.BigEndian.Uint32(lenBuf)
		entryData := make([]byte, entryLen)
		_, err = io.ReadFull(reader, entryData)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to read entry data")
			return -1, false
		}

		// Verify CRC
		storedCRC := binary.BigEndian.Uint32(entryData[:crc32ChecksumLength])
		calculatedCRC := crc32.ChecksumIEEE(entryData[crc32ChecksumLength:])
		if storedCRC != calculatedCRC {
			s.logger.Error().Msg("CRC mismatch")
			return -1, false
		}

		// Rebuild index entry
		entry, err := s.wal.deserializeEntry(entryData, offset)
		if err != nil {
			s.logger.Error().Err(err).Msg("Failed to deserialize entry")
			return -1, false
		}

		indexEntry := IndexEntry{
			Offset:       offset,
			FilePosition: currentPos,
			Size:         int32(entryLengthHeaderSize + entryLen),
			Timestamp:    entry.Timestamp,
		}
		if err := s.index.Add(indexEntry); err != nil {
			s.logger.Error().Err(err).Msg("Failed to add entry to index during scan")
			return -1, false
		}

		currentPos += int64(entryLengthHeaderSize + entryLen)
		offset++
	}

	s.size = currentPos
	if s.index.Count() > 0 {
		s.startOffset = s.index.entries[0].Offset
		s.endOffset = s.index.entries[s.index.Count()-1].Offset
	}

	s.logger.Info().
		Int64("start_offset", s.startOffset).
		Int64("end_offset", s.endOffset).
		Int("entries", s.index.Count()).
		Msg("Segment scan complete")

	return s.endOffset, true
}
