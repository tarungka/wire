package store

import (
	"errors"
	"io"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/new/db"
	"github.com/tarungka/wire/internal/rsync"
)

var (
	// ErrStoreNotOpen is returned when a Store is not open.
	ErrStoreNotOpen = errors.New("store not open")

	// ErrStoreOpen is returned when a Store is already open.
	ErrStoreOpen = errors.New("store already open")

	// ErrNotReady is returned when a Store is not ready to accept requests.
	ErrNotReady = errors.New("store not ready")

	// ErrNotLeader is returned when a node attempts to execute a leader-only
	// operation.
	ErrNotLeader = errors.New("not leader")

	// ErrNotSingleNode is returned when a node attempts to execute a single-node
	// only operation.
	ErrNotSingleNode = errors.New("not single-node")

	// ErrStaleRead is returned if the executing the query would violate the
	// requested freshness.
	ErrStaleRead = errors.New("stale read")

	// ErrOpenTimeout is returned when the Store does not apply its initial
	// logs within the specified time.
	ErrOpenTimeout = errors.New("timeout waiting for initial logs application")

	// ErrWaitForRemovalTimeout is returned when the Store does not confirm removal
	// of a node within the specified time.
	ErrWaitForRemovalTimeout = errors.New("timeout waiting for node removal confirmation")

	// ErrWaitForLeaderTimeout is returned when the Store cannot determine the leader
	// within the specified time.
	ErrWaitForLeaderTimeout = errors.New("timeout waiting for leader")

	// ErrInvalidBackupFormat is returned when the requested backup format
	// is not valid.
	ErrInvalidBackupFormat = errors.New("invalid backup format")

	// ErrInvalidVacuumFormat is returned when the requested backup format is not
	// compatible with vacuum.
	ErrInvalidVacuum = errors.New("invalid vacuum")

	// ErrLoadInProgress is returned when a load is already in progress and the
	// requested operation cannot be performed.
	ErrLoadInProgress = errors.New("load in progress")

	// ErrNotImplemented when there is no implementation of the function
	// will only exits until this application in under development
	ErrNotImplemented = errors.New("not implemented")

	// ErrDatabaseNotOpen when the database is closed
	ErrDatabaseNotOpen = errors.New("database is not open")
)

type Config struct {
	Dir string // The working directory for raft.
	ID  string // Node ID.

	DatabaseType string // can be one of: badgerdb, rocksdb
}

type StateMachine struct {
	open      *rsync.AtomicBool
	raftDir   string
	peersPath string

	// FSM
	db    *badger.DB
	fsmMu sync.Mutex

	// Stable Store
	raftStable raft.StableStore // Persistent k-v store.

	// Log Store
	raftLog raft.LogStore // Persistent log store.

	// physical store containing info about the the stable and the log store
	dbStore db.DbStore // currently supported are badgerDB, rocksDB and boltDB

	logger zerolog.Logger
}

func New(c *Config) (*StateMachine, error) {
	newLogger := logger.GetLogger("store")
	newLogger.Print("creating new store")
	dbConfig := db.Config{
		Dir: c.Dir,
	}
	newDbStore, err := db.New(c.DatabaseType, &dbConfig)
	if err != nil {
		return nil, err
	}
	return &StateMachine{
		open:    rsync.NewAtomicBool(),
		logger:  newLogger,
		dbStore: newDbStore,
	}, nil
}

// Impl of the raft FSM
var _ raft.FSM = (*StateMachine)(nil)

func (s *StateMachine) Apply(*raft.Log) interface{} {
	return ErrNotImplemented
}

func (s *StateMachine) Snapshot() (raft.FSMSnapshot, error) {
	return nil, ErrNotImplemented
}

func (s *StateMachine) Restore(snapshot io.ReadCloser) error {
	return ErrNotImplemented
}

// Impl of the raft Stable Store
var _ raft.StableStore = (*StateMachine)(nil)

func (s *StateMachine) Set(key, val []byte) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	err := s.dbStore.Set(key, val)
	if err != nil {
		s.logger.Err(err).Msg("error setting value in store")
		return err
	}
	return nil
}

// Get returns the value for key, or an empty byte slice if key was not found.
func (s *StateMachine) Get(key []byte) ([]byte, error) {
	if !s.open.Is() {
		return nil, ErrStoreNotOpen
	}
	val, err := s.dbStore.Get(key)
	if err != nil {
		s.logger.Err(err).Msg("error setting value in store")
		return nil, err
	}
	return val, nil
}

func (s *StateMachine) SetUint64(key []byte, val uint64) error {
	if !s.open.Is() {
		return ErrStoreNotOpen
	}
	err := s.dbStore.SetUint64(key, val)
	if err != nil {
		s.logger.Err(err).Msg("error setting value in store")
		return err
	}
	return nil
}

// GetUint64 returns the uint64 value for key, or 0 if key was not found.
func (s *StateMachine) GetUint64(key []byte) (uint64, error) {
	if !s.open.Is() {
		return 0, ErrStoreNotOpen
	}
	val, err := s.dbStore.GetUint64(key)
	if err != nil {
		s.logger.Err(err).Msg("error setting value in store")
		return 0, err
	}
	return val, nil
}

// Impl of the raft Log Store
var _ raft.LogStore = (*StateMachine)(nil)

// FirstIndex returns the first index written. 0 for no entries.
func (s *StateMachine) FirstIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// LastIndex returns the last index written. 0 for no entries.
func (s *StateMachine) LastIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// GetLog gets a log entry at a given index.
func (s *StateMachine) GetLog(index uint64, log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLog stores a log entry.
func (s *StateMachine) StoreLog(log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLogs stores multiple log entries. By default the logs stored may not be contiguous with previous logs (i.e. may have a gap in Index since the last log written). If an implementation can't tolerate this it may optionally implement `MonotonicLogStore` to indicate that this is not allowed. This changes Raft's behaviour after restoring a user snapshot to remove all previous logs instead of relying on a "gap" to signal the discontinuity between logs before the snapshot and logs after.
func (s *StateMachine) StoreLogs(logs []*raft.Log) error {
	return ErrNotImplemented
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (s *StateMachine) DeleteRange(min, max uint64) error {
	return ErrNotImplemented
}