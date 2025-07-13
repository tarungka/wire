package wal

import (
	"sync/atomic"
	"time"
)

// WALMetrics holds metrics for the WAL.
// These are designed to be thread-safe for concurrent access.
type WALMetrics struct {
	// Append metrics
	appendCalls   uint64 // Total number of calls to Append()
	appendErrors  uint64 // Number of failed Append() calls
	appendSuccess uint64 // Number of successful Append() calls
	entriesAppended uint64 // Total number of entries successfully appended
	bytesAppended uint64 // Total number of bytes (on-disk size) successfully appended
	appendLatency uint64 // Total latency of successful appends in nanoseconds

	// Sync metrics
	syncCalls uint64 // Number of explicit sync calls
	syncErrors uint64 // Number of failed sync calls

	// Segment metrics
	segmentsCreated uint64 // Number of segments created
	segmentsDeleted uint64 // Number of segments deleted due to retention

	// Recovery metrics
	recoveryCalls   uint64 // Number of times recovery process was initiated
	recoveryErrors  uint64 // Number of failed recovery attempts
	recoverySuccess uint64 // Number of successful recovery attempts
	segmentsScannedDuringRecovery uint64 // Number of segment files scanned during recovery
	entriesRecoveredDuringRecovery uint64 // Total number of valid entries found during recovery
	corruptedSegmentsDuringRecovery uint64 // Number of segments marked as corrupted during recovery
	recoveryDuration uint64 // Total latency of successful recoveries in nanoseconds
}

// NewWALMetrics initializes a new WALMetrics struct.
func NewWALMetrics() *WALMetrics {
	return &WALMetrics{}
}

// --- Append Metrics ---

func (m *WALMetrics) IncAppendCalls() {
	atomic.AddUint64(&m.appendCalls, 1)
}

func (m *WALMetrics) IncAppendErrors() {
	atomic.AddUint64(&m.appendErrors, 1)
}

func (m *WALMetrics) IncAppendSuccess(entries uint64, bytes int64, latency time.Duration) {
	atomic.AddUint64(&m.appendSuccess, 1)
	atomic.AddUint64(&m.entriesAppended, entries)
	atomic.AddUint64(&m.bytesAppended, uint64(bytes))
	atomic.AddUint64(&m.appendLatency, uint64(latency.Nanoseconds()))
}

// --- Sync Metrics ---

func (m *WALMetrics) IncSyncCalls() {
	atomic.AddUint64(&m.syncCalls, 1)
}

func (m *WALMetrics) IncSyncErrors() {
	atomic.AddUint64(&m.syncErrors, 1)
}

// --- Segment Metrics ---

func (m *WALMetrics) IncSegmentsCreated() {
	atomic.AddUint64(&m.segmentsCreated, 1)
}

func (m *WALMetrics) IncSegmentsDeleted() {
	atomic.AddUint64(&m.segmentsDeleted, 1)
}

// --- Recovery Metrics ---

func (m *WALMetrics) IncRecoveryCalls() {
	atomic.AddUint64(&m.recoveryCalls, 1)
}

func (m *WALMetrics) IncRecoveryErrors() {
	atomic.AddUint64(&m.recoveryErrors, 1)
}

func (m *WALMetrics) IncRecoverySuccess(duration time.Duration, segmentsScanned, entriesRecovered, corruptedSegments uint64) {
	atomic.AddUint64(&m.recoverySuccess, 1)
	atomic.AddUint64(&m.recoveryDuration, uint64(duration.Nanoseconds()))
	atomic.AddUint64(&m.segmentsScannedDuringRecovery, segmentsScanned)
	atomic.AddUint64(&m.entriesRecoveredDuringRecovery, entriesRecovered)
	atomic.AddUint64(&m.corruptedSegmentsDuringRecovery, corruptedSegments)
}
