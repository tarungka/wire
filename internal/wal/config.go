package wal

import "time"

// WALConfig configures the Write-Ahead Log.
type WALConfig struct {
	// Directory is the path where WAL segment files will be stored.
	Directory string `yaml:"directory"`

	// SegmentSize is the maximum size of a single WAL segment file in bytes.
	// Defaults to 1GB.
	SegmentSize int64 `yaml:"segment_size"`

	// MaxSegments is the maximum number of WAL segments to retain.
	// Older segments beyond this limit will be deleted.
	// Defaults to 10.
	MaxSegments int `yaml:"max_segments"`

	// SyncInterval is the duration between forced file system syncs of the current segment.
	// A smaller interval provides stronger durability guarantees but may impact performance.
	// Defaults to 100ms. 0 means sync on every write (if FlushOnWrite is also true).
	SyncInterval time.Duration `yaml:"sync_interval"`

	// Compression is the type of compression to use for WAL entries.
	// Supported values: "none", "snappy", "zstd".
	// Defaults to "none".
	Compression string `yaml:"compression"`

	// FlushOnWrite determines if every append operation should trigger an immediate flush
	// of the segment writer's buffer to the OS. If true, it provides higher durability
	// at the cost of performance. If false, flushing relies on the SyncInterval or
	// buffer filling up.
	// Defaults to false.
	FlushOnWrite bool `yaml:"flush_on_write"`

	// Enabled indicates if WAL is enabled for the pipeline.
	Enabled bool `yaml:"enabled"`
}

// DefaultWALConfig returns a WALConfig with default values.
func DefaultWALConfig() WALConfig {
	return WALConfig{
		Directory:    "data/wal", // Default directory, should be configurable
		SegmentSize:  1 * 1024 * 1024 * 1024, // 1GB
		MaxSegments:  10,
		SyncInterval: 100 * time.Millisecond,
		Compression:  "none",
		FlushOnWrite: false,
		Enabled:      true, // Assuming WAL is enabled by default if configured
	}
}
