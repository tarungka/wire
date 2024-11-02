package badgerdb

import (
	"encoding/binary"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	db *badger.DB
	mu sync.RWMutex
}

func New(c *Config) *DB {
	newLogger := logger.GetLogger("baddb")
	newLogger.Print("creating new store")
	return &DB{
		dbPath: c.Dir,
		logger: newLogger,
	}
}

func (db *DB) Open(path string) (*badger.DB, error) {
	if path == "" {
		path = "/tmp/badger"
	}

	badgerDB, err := badger.Open(badger.DefaultOptions(path))
	if err != nil {
		return nil, err
	}
	db.open.Set()
	log.Debug().Msgf("opened a file-based database at %s", path)

	return badgerDB, nil
}

// Opens an in memory database
func (db *DB) OpenInMemory() (*badger.DB, error) {
	badgerInMemory, err := badger.Open(badger.DefaultOptions("").WithInMemory(true))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("opened a in-memory database")

	return badgerInMemory, nil
}

func (db *DB) Set(key, val []byte) error {
	if !db.open.Is() {
		return ErrDBNotOpen
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	db.logger.Trace().Msgf("setting value of key %v to %v", key, val)
	err := db.db.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	if err != nil {
		db.logger.Err(err).Msgf("err setting value of key %v to %v", key, val)
		return err
	}
	return nil
}

// Get returns the value for key, or an empty byte slice if key was not found.
func (db *DB) Get(key []byte) ([]byte, error) {
	if !db.open.Is() {
		return nil, ErrDBNotOpen
	}
	db.mu.RLock()
	defer db.mu.RUnlock()

	var val []byte
	err := db.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		val, err = item.ValueCopy(nil)
		return err
	})
	if err != nil {
		db.logger.Err(err).Msgf("err setting value of key %v to %v", key, val)
		return nil, err
	}
	return val, nil
}

func (db *DB) SetUint64(key []byte, val uint64) error {
	if !db.open.Is() {
		return ErrDBNotOpen
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	db.logger.Trace().Msgf("setting value of key %v to %v", key, val)
	err := db.db.Update(func(txn *badger.Txn) error {
		buf := make([]byte, 8)               // 8*8=64
		binary.BigEndian.PutUint64(buf, val) // write the contents of val into buf
		err := txn.Set(key, buf)
		return err
	})
	if err != nil {
		db.logger.Err(err).Msgf("err setting value of key %v to %v", key, val)
		return err
	}
	return nil
}

// GetUint64 returns the uint64 value for key, or 0 if key was not found.
func (db *DB) GetUint64(key []byte) (uint64, error) {
	if !db.open.Is() {
		return 0, ErrDBNotOpen
	}
	db.mu.RLock()
	defer db.mu.RUnlock()

	var val uint64
	err := db.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		err = item.Value(func(data []byte) error {
			val = binary.BigEndian.Uint64(data)
			return nil
		})
		return err
	})
	if err != nil {
		db.logger.Err(err).Msgf("err setting value of key %v to %v", key, val)
		return 0, err
	}
	return val, nil
}

// FirstIndex returns the first index written. 0 for no entries.
func (db *DB) FirstIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// LastIndex returns the last index written. 0 for no entries.
func (db *DB) LastIndex() (uint64, error) {
	return 0, ErrNotImplemented
}

// GetLog gets a log entry at a given index.
func (db *DB) GetLog(index uint64, log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLog stores a log entry.
func (db *DB) StoreLog(log *raft.Log) error {
	return ErrNotImplemented
}

// StoreLogs stores multiple log entries. By default the logs stored may not be contiguous with previous logs (i.e. may have a gap in Index since the last log written). If an implementation can't tolerate this it may optionally implement `MonotonicLogStore` to indicate that this is not allowed. This changes Raft's behaviour after restoring a user snapshot to remove all previous logs instead of relying on a "gap" to signal the discontinuity between logs before the snapshot and logs after.
func (db *DB) StoreLogs(logs []*raft.Log) error {
	return ErrNotImplemented
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (db *DB) DeleteRange(min, max uint64) error {
	return ErrNotImplemented
}
