package badgerdb

import (
	"bytes"
	"encoding/binary"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/hashicorp/raft"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
	utils "github.com/tarungka/wire/internal/utils"
)

type Config struct {
	Dir string
}

type DB struct {
	open rsync.AtomicBool

	dbPath string
	logger zerolog.Logger

	db *badger.DB
	// Since badgerDB uses MVCC we need to manage concurrency
	// at the application level; as opposed to Bbolt which uses
	// SWMR where this is not the case
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
	db.logger.Trace().Msgf("setting value of key %v to %v", key, val)

	db.mu.Lock()
	defer db.mu.Unlock()

	err := db.db.Update(func(txn *badger.Txn) error {
		err := txn.Set(key, val)
		return err
	})
	return err
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
		// buf := make([]byte, 8) // 8*8=64
		// binary.BigEndian.PutUint64(buf, val) // write the contents of val into buf
		buf := utils.ConvertUint64ToBytes(val)
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
	return val, err
}

// FirstIndex returns the first index written. 0 for no entries.
func (db *DB) FirstIndex() (uint64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var resp []byte
	var err error
	err = db.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{}
		opts.PrefetchValues = true
		opts.Reverse = false
		itr := txn.NewIterator(opts)
		defer itr.Close()

		itr.Rewind()
		if itr.Valid() {
			item := itr.Item()
			resp = item.KeyCopy(nil)
		} else {
			resp = bytes.Repeat([]byte{0}, 8)
		}
		return err
	})
	return utils.ConvertBytesToUint64(resp), err
}

// LastIndex returns the last index written. 0 for no entries.
func (db *DB) LastIndex() (uint64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var resp []byte
	var err error
	err = db.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{}
		opts.PrefetchValues = true
		opts.Reverse = true
		itr := txn.NewIterator(opts)
		defer itr.Close()

		itr.Rewind() // as reverse is true, rewind will point to the latest log
		if itr.Valid() {
			item := itr.Item()
			resp = item.KeyCopy(nil)
		} else {
			resp = bytes.Repeat([]byte{0}, 8)
		}
		return err
	})
	return utils.ConvertBytesToUint64(resp), err
}

// GetLog gets a log entry at a given index.
func (db *DB) GetLog(index uint64, log *raft.Log) error {
	db.mu.RLock()
	defer db.mu.RUnlock()

	resp, err := db.Get(utils.ConvertUint64ToBytes(index))
	if err != nil {
		return err
	}
	return utils.DecodeMsgPack(resp, log)
}

// StoreLog stores a log entry.
func (db *DB) StoreLog(log *raft.Log) error {
	return db.StoreLogs([]*raft.Log{log})
}

// StoreLogs stores multiple log entries. By default the logs stored may not be contiguous with previous logs (i.e. may have a gap in Index since the last log written). If an implementation can't tolerate this it may optionally implement `MonotonicLogStore` to indicate that this is not allowed. This changes Raft's behaviour after restoring a user snapshot to remove all previous logs instead of relying on a "gap" to signal the discontinuity between logs before the snapshot and logs after.
func (db *DB) StoreLogs(logs []*raft.Log) (retErr error) {
	// Writing this defer function here to free the lock before the costly
	// logging operation
	defer func() {
		if retErr != nil {
			db.logger.Err(retErr).Msg("error when encoding msgpack")
		}
	}()

	db.mu.Lock()
	defer db.mu.Unlock()
	for _, l := range logs {
		key := utils.ConvertUint64ToBytes(l.Index)
		val, err := utils.EncodeMsgPack(l)
		if err != nil {
			return err
		}
		db.Set(key, val.Bytes())
	}
	// TODO: add this to metrics
	// writeCapacity := (float32(1_000_000_000)/float32(time.Since(now).Nanoseconds()))*float32(len(logs))
	return nil
}

// DeleteRange deletes a range of log entries. The range is inclusive.
func (db *DB) DeleteRange(min, max uint64) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.db.Update(func(txn *badger.Txn) error {
		for i := min; i <= max; i++ {
			if err := txn.Delete(utils.ConvertUint64ToBytes(i)); err != nil {
				return err
			}
		}
		return nil
	})
	return nil
}

func (db *DB) Sync() error {
	return db.db.Sync()
}
