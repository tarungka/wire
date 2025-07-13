package wal

import (
	"sync/atomic"
	"time"
)

// WALMetrics tracks metrics for WAL operations.
type WALMetrics struct {
	// Write metrics
	WritesTotal       atomic.Int64
	WritesFailedTotal atomic.Int64
	BytesWrittenTotal atomic.Int64
	WriteLatencyNanos atomic.Int64
	LastWriteTime     atomic.Int64 // Unix nano timestamp

	// Read metrics
	ReadsTotal       atomic.Int64
	ReadsFailedTotal atomic.Int64
	BytesReadTotal   atomic.Int64
	ReadLatencyNanos atomic.Int64

	// Segment metrics
	SegmentsCreatedTotal atomic.Int64
	SegmentsDeletedTotal atomic.Int64
	SegmentsRotatedTotal atomic.Int64
	CurrentSegmentSize   atomic.Int64
	TotalSegments        atomic.Int32

	// Sync metrics
	SyncsTotal       atomic.Int64
	SyncsFailedTotal atomic.Int64
	SyncLatencyNanos atomic.Int64
	LastSyncTime     atomic.Int64 // Unix nano timestamp

	// Compression metrics
	CompressionRatio     atomic.Int64 // Percentage * 100 (e.g., 7523 = 75.23%)
	BytesCompressedTotal atomic.Int64
	CompressionTimeNanos atomic.Int64

	// Recovery metrics
	RecoveriesTotal       atomic.Int64
	RecoveriesFailedTotal atomic.Int64
	RecoveryTimeNanos     atomic.Int64
	EntriesRecoveredTotal atomic.Int64

	// Error metrics
	CorruptedEntriesTotal atomic.Int64
	CRCMismatchesTotal    atomic.Int64

	// Resource metrics
	OpenFileDescriptors atomic.Int32
	DiskUsageBytes      atomic.Int64
}

// NewWALMetrics creates a new WALMetrics instance.
func NewWALMetrics() *WALMetrics {
	return &WALMetrics{}
}

// RecordWrite records a write operation.
func (m *WALMetrics) RecordWrite(bytes int64, latency time.Duration, success bool) {
	m.WritesTotal.Add(1)
	if !success {
		m.WritesFailedTotal.Add(1)
	} else {
		m.BytesWrittenTotal.Add(bytes)
		m.WriteLatencyNanos.Store(latency.Nanoseconds())
		m.LastWriteTime.Store(time.Now().UnixNano())
	}
}

// RecordRead records a read operation.
func (m *WALMetrics) RecordRead(bytes int64, latency time.Duration, success bool) {
	m.ReadsTotal.Add(1)
	if !success {
		m.ReadsFailedTotal.Add(1)
	} else {
		m.BytesReadTotal.Add(bytes)
		m.ReadLatencyNanos.Store(latency.Nanoseconds())
	}
}

// RecordSync records a sync operation.
func (m *WALMetrics) RecordSync(latency time.Duration, success bool) {
	m.SyncsTotal.Add(1)
	if !success {
		m.SyncsFailedTotal.Add(1)
	} else {
		m.SyncLatencyNanos.Store(latency.Nanoseconds())
		m.LastSyncTime.Store(time.Now().UnixNano())
	}
}

// RecordSegmentCreated records the creation of a new segment.
func (m *WALMetrics) RecordSegmentCreated() {
	m.SegmentsCreatedTotal.Add(1)
	m.TotalSegments.Add(1)
}

// RecordSegmentDeleted records the deletion of a segment.
func (m *WALMetrics) RecordSegmentDeleted() {
	m.SegmentsDeletedTotal.Add(1)
	m.TotalSegments.Add(-1)
}

// RecordSegmentRotated records a segment rotation.
func (m *WALMetrics) RecordSegmentRotated() {
	m.SegmentsRotatedTotal.Add(1)
}

// UpdateSegmentSize updates the current segment size.
func (m *WALMetrics) UpdateSegmentSize(size int64) {
	m.CurrentSegmentSize.Store(size)
}

// RecordCompression records compression metrics.
func (m *WALMetrics) RecordCompression(originalSize, compressedSize int64, latency time.Duration) {
	if originalSize > 0 {
		ratio := (originalSize - compressedSize) * 10000 / originalSize
		m.CompressionRatio.Store(ratio)
	}
	m.BytesCompressedTotal.Add(compressedSize)
	m.CompressionTimeNanos.Store(latency.Nanoseconds())
}

// RecordRecovery records recovery metrics.
func (m *WALMetrics) RecordRecovery(entriesRecovered int64, latency time.Duration, success bool) {
	m.RecoveriesTotal.Add(1)
	if !success {
		m.RecoveriesFailedTotal.Add(1)
	} else {
		m.EntriesRecoveredTotal.Add(entriesRecovered)
		m.RecoveryTimeNanos.Store(latency.Nanoseconds())
	}
}

// RecordCorruptedEntry records a corrupted entry.
func (m *WALMetrics) RecordCorruptedEntry() {
	m.CorruptedEntriesTotal.Add(1)
}

// RecordCRCMismatch records a CRC mismatch.
func (m *WALMetrics) RecordCRCMismatch() {
	m.CRCMismatchesTotal.Add(1)
}

// UpdateOpenFileDescriptors updates the number of open file descriptors.
func (m *WALMetrics) UpdateOpenFileDescriptors(count int32) {
	m.OpenFileDescriptors.Store(count)
}

// UpdateDiskUsage updates the disk usage in bytes.
func (m *WALMetrics) UpdateDiskUsage(bytes int64) {
	m.DiskUsageBytes.Store(bytes)
}

// Snapshot returns a snapshot of all metrics.
func (m *WALMetrics) Snapshot() WALMetricsSnapshot {
	return WALMetricsSnapshot{
		WritesTotal:       m.WritesTotal.Load(),
		WritesFailedTotal: m.WritesFailedTotal.Load(),
		BytesWrittenTotal: m.BytesWrittenTotal.Load(),
		WriteLatencyNanos: m.WriteLatencyNanos.Load(),
		LastWriteTime:     time.Unix(0, m.LastWriteTime.Load()),

		ReadsTotal:       m.ReadsTotal.Load(),
		ReadsFailedTotal: m.ReadsFailedTotal.Load(),
		BytesReadTotal:   m.BytesReadTotal.Load(),
		ReadLatencyNanos: m.ReadLatencyNanos.Load(),

		SegmentsCreatedTotal: m.SegmentsCreatedTotal.Load(),
		SegmentsDeletedTotal: m.SegmentsDeletedTotal.Load(),
		SegmentsRotatedTotal: m.SegmentsRotatedTotal.Load(),
		CurrentSegmentSize:   m.CurrentSegmentSize.Load(),
		TotalSegments:        m.TotalSegments.Load(),

		SyncsTotal:       m.SyncsTotal.Load(),
		SyncsFailedTotal: m.SyncsFailedTotal.Load(),
		SyncLatencyNanos: m.SyncLatencyNanos.Load(),
		LastSyncTime:     time.Unix(0, m.LastSyncTime.Load()),

		CompressionRatio:     float64(m.CompressionRatio.Load()) / 100.0,
		BytesCompressedTotal: m.BytesCompressedTotal.Load(),
		CompressionTimeNanos: m.CompressionTimeNanos.Load(),

		RecoveriesTotal:       m.RecoveriesTotal.Load(),
		RecoveriesFailedTotal: m.RecoveriesFailedTotal.Load(),
		RecoveryTimeNanos:     m.RecoveryTimeNanos.Load(),
		EntriesRecoveredTotal: m.EntriesRecoveredTotal.Load(),

		CorruptedEntriesTotal: m.CorruptedEntriesTotal.Load(),
		CRCMismatchesTotal:    m.CRCMismatchesTotal.Load(),

		OpenFileDescriptors: m.OpenFileDescriptors.Load(),
		DiskUsageBytes:      m.DiskUsageBytes.Load(),
	}
}

// WALMetricsSnapshot represents a point-in-time snapshot of WAL metrics.
type WALMetricsSnapshot struct {
	// Write metrics
	WritesTotal       int64
	WritesFailedTotal int64
	BytesWrittenTotal int64
	WriteLatencyNanos int64
	LastWriteTime     time.Time

	// Read metrics
	ReadsTotal       int64
	ReadsFailedTotal int64
	BytesReadTotal   int64
	ReadLatencyNanos int64

	// Segment metrics
	SegmentsCreatedTotal int64
	SegmentsDeletedTotal int64
	SegmentsRotatedTotal int64
	CurrentSegmentSize   int64
	TotalSegments        int32

	// Sync metrics
	SyncsTotal       int64
	SyncsFailedTotal int64
	SyncLatencyNanos int64
	LastSyncTime     time.Time

	// Compression metrics
	CompressionRatio     float64
	BytesCompressedTotal int64
	CompressionTimeNanos int64

	// Recovery metrics
	RecoveriesTotal       int64
	RecoveriesFailedTotal int64
	RecoveryTimeNanos     int64
	EntriesRecoveredTotal int64

	// Error metrics
	CorruptedEntriesTotal int64
	CRCMismatchesTotal    int64

	// Resource metrics
	OpenFileDescriptors int32
	DiskUsageBytes      int64
}
