package rocksdb

import (
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
)

type Config struct {
	Dir string
}

type DB struct {
	open rsync.AtomicBool
	dbPath string
	logger zerolog.Logger
}

func New(c *Config) *DB {
	newLogger := logger.GetLogger("baddb")
	newLogger.Print("creating new store")
	return &DB{
		dbPath: c.Dir,
		logger: newLogger,
	}
}

func (db *DB) Open(path string) error {
	return ErrNotImplemented
}

// Opens a in memory database
func (db *DB) OpenInMemory() error {
	return ErrNotImplemented
}

func (db *DB) Set(key, val []byte) error {
	return ErrNotImplemented
}

// Get returns the value for key, or an empty byte slice if key was not found.
func (s *DB) Get(key []byte) ([]byte, error) {
	return nil, ErrNotImplemented
}

func (s *DB) SetUint64(key []byte, val uint64) error {
	return ErrNotImplemented
}

// GetUint64 returns the uint64 value for key, or 0 if key was not found.
func (s *DB) GetUint64(key []byte) (uint64, error) {
	return 0, ErrNotImplemented
}

// FirstIndex returns the first index written. 0 for no entries.
func (s *DB) FirstIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// LastIndex returns the last index written. 0 for no entries.
func (s *DB) LastIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// GetLog gets a log entry at a given index.
func (s *DB) GetLog(index uint64, log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLog stores a log entry.
func (s *DB) StoreLog(log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLogs stores multiple log entries. By default the logs stored may not be contiguous with previous logs (i.e. may have a gap in Index since the last log written). If an implementation can't tolerate this it may optionally implement `MonotonicLogStore` to indicate that this is not allowed. This changes Raft's behaviour after restoring a user snapshot to remove all previous logs instead of relying on a "gap" to signal the discontinuity between logs before the snapshot and logs after.
func (s *DB) StoreLogs(logs []*raft.Log) error {
	return ErrNotImplemented
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (s *DB) DeleteRange(min, max uint64) error {
	return ErrNotImplemented
}

func (s *DB) Close() error {
	return nil
}

func (db *DB) Stats() (map[string]interface{}, error) {
	if !db.open.Is() {
		return nil, ErrDBNotOpen
	}

	return nil, ErrNotImplemented
}
