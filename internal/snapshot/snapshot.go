package snapshot

import (
	"io"

	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

// Snapshot represents a snapshot of the database state.
type Snapshot struct {
	rc io.ReadCloser
	// logger *log.Logger
	logger zerolog.Logger
}

// NewSnapshot creates a new snapshot.
func NewSnapshot(rc io.ReadCloser) *Snapshot {
	return &Snapshot{
		rc: rc,
		// logger: log.New(log.Writer(), "[snapshot] ", log.LstdFlags),
		logger: logger.GetLogger("snapshot"),
	}
}

import (
	"fmt"
	"io"
	"time"

	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

// Snapshot represents a snapshot of the database state.
type Snapshot struct {
	rc io.ReadCloser
	// logger *log.Logger
	logger zerolog.Logger
}

// NewSnapshot creates a new snapshot.
func NewSnapshot(rc io.ReadCloser) *Snapshot {
	return &Snapshot{
		rc: rc,
		// logger: log.New(log.Writer(), "[snapshot] ", log.LstdFlags),
		logger: logger.GetLogger("snapshot"),
	}
}

// Persist writes the snapshot to the given sink.
func (s *Snapshot) Persist(sink raft.SnapshotSink) (retErr error) {
	s.logger.Info().Str("sink_id", sink.ID()).Msg("starting snapshot persistence")
	startTime := time.Now()

	// Ensure s.rc is closed. This is deferred first to catch all scenarios.
	defer func() {
		if err := s.rc.Close(); err != nil {
			s.logger.Error().Err(err).Str("sink_id", sink.ID()).Msg("failed to close snapshot reader")
			// If retErr is nil, we might want to return this error,
			// but the primary errors are from copy and sink.Close().
			// For now, just log it, as io.Copy or sink.Close errors are more critical.
		}
	}()

	var bytesWritten int64
	var copyErr error

	bytesWritten, copyErr = io.Copy(sink, s.rc)

	// Attempt to close the sink regardless of copyError.
	// Raft expects the sink to be closed.
	sinkCloseErr := sink.Close()

	duration := time.Since(startTime)

	if copyErr != nil {
		s.logger.Error().Err(copyErr).Str("sink_id", sink.ID()).Int64("bytes_written", bytesWritten).Dur("duration_ms", duration).Msg("failed to persist snapshot")
		// If sink.Close() also errored, log it too. copyErr takes precedence for return.
		if sinkCloseErr != nil {
			s.logger.Error().Err(sinkCloseErr).Str("sink_id", sink.ID()).Msg("failed to close snapshot sink after copy error")
		}
		return copyErr // Return the io.Copy error
	}

	// If copy was successful, check sink.Close() error.
	if sinkCloseErr != nil {
		s.logger.Error().Err(sinkCloseErr).Str("sink_id", sink.ID()).Int64("bytes_written", bytesWritten).Dur("duration_ms", duration).Msg("failed to close snapshot sink after successful copy")
		return sinkCloseErr // Return the sink.Close() error
	}

	s.logger.Info().Str("sink_id", sink.ID()).Int64("bytes_written", bytesWritten).Dur("duration_ms", duration).Msg("successfully persisted snapshot")
	return nil // Both copy and sink.Close() were successful
}

// Release releases the snapshot.
func (s *Snapshot) Release() {
	s.logger.Debug().Msg("snapshot release called")
	// Ensure that the source data for the snapshot is closed regardless of
	// whether the snapshot is persisted or not.
	// If s.rc is already closed (e.g., after Persist), closing it again
	// might lead to an error (e.g., "file already closed").
	// However, io.Closer interface implies that Close() should be idempotent,
	// or at least safe to call multiple times. Standard library types like *os.File
	// handle this. If s.rc is a custom type, it should ideally also be robust to this.
	if err := s.rc.Close(); err != nil {
		// Log the error, but don't propagate it as Release is a cleanup operation.
		s.logger.Error().Err(err).Msg("error closing snapshot reader during release")
	}
}
