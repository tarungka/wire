package rocksdb

import (
	"errors"
	"sync"

	"github.com/hashicorp/raft"
	"github.com/linxGnu/grocksdb"
	"github.com/rs/zerolog"
	dberrors "github.com/tarungka/wire/internal/errors"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/rsync"
	"github.com/tarungka/wire/internal/utils"
)

var ErrKeyNotFound = errors.New("key not found")

// Config holds the configuration for the RocksDB store.
type Config struct {
	Dir string
}

// DB is a RocksDB implementation of the DbStore interface.
type DB struct {
	open   rsync.AtomicBool
	dbPath string
	logger zerolog.Logger
	db     *grocksdb.DB
	mu     sync.RWMutex
}

// New creates a new RocksDB store.
func New(c *Config) *DB {
	newLogger := logger.GetLogger("rocksdb")
	newLogger.Print("creating new rocksdb")
	return &DB{
		dbPath: c.Dir,
		logger: newLogger,
	}
}

// Open opens the RocksDB database.
func (db *DB) Open() error {
	if db.open.Is() {
		return dberrors.ErrDBOpen
	}
	if db.dbPath == "" {
		db.dbPath = "/tmp/rocksdb"
	}

	opts := grocksdb.NewDefaultOptions()
	opts.SetCreateIfMissing(true)

	rocksDB, err := grocksdb.OpenDb(opts, db.dbPath)
	if err != nil {
		return err
	}

	db.db = rocksDB
	db.open.Set()
	db.logger.Debug().Msgf("opened a file-based database at %s", db.dbPath)
	return nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if !db.open.Is() {
		return dberrors.ErrDBNotOpen
	}
	db.db.Close()
	db.open.Unset()
	return nil
}

// Set stores a key-value pair.
func (db *DB) Set(key, val []byte) error {
	if !db.open.Is() {
		return dberrors.ErrDBNotOpen
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	wo := grocksdb.NewDefaultWriteOptions()
	defer wo.Destroy()
	return db.db.Put(wo, key, val)
}

// Get retrieves the value for a key.
func (db *DB) Get(key []byte) ([]byte, error) {
	if !db.open.Is() {
		return nil, dberrors.ErrDBNotOpen
	}
	db.mu.RLock()
	defer db.mu.RUnlock()

	ro := grocksdb.NewDefaultReadOptions()
	defer ro.Destroy()

	slice, err := db.db.Get(ro, key)
	if err != nil {
		return nil, err
	}
	if !slice.Exists() {
		return nil, ErrKeyNotFound
	}
	defer slice.Free()

	// ValueCopy is needed because the slice data is only valid until Free is called.
	val := make([]byte, len(slice.Data()))
	copy(val, slice.Data())
	return val, nil
}

// SetUint64 stores a uint64 value for a key.
func (db *DB) SetUint64(key []byte, val uint64) error {
	return db.Set(key, utils.ConvertUint64ToBytes(val))
}

// GetUint64 retrieves a uint64 value for a key.
func (db *DB) GetUint64(key []byte) (uint64, error) {
	val, err := db.Get(key)
	if err != nil {
		return 0, err
	}
	return utils.ConvertBytesToUint64(val), nil
}

// FirstIndex returns the first index written.
func (db *DB) FirstIndex() (uint64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	ro := grocksdb.NewDefaultReadOptions()
	defer ro.Destroy()
	it := db.db.NewIterator(ro)
	defer it.Close()

	it.SeekToFirst()
	if it.Valid() {
		return utils.ConvertBytesToUint64(it.Key().Data()), nil
	}
	return 0, nil
}

// LastIndex returns the last index written.
func (db *DB) LastIndex() (uint64, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	ro := grocksdb.NewDefaultReadOptions()
	defer ro.Destroy()
	it := db.db.NewIterator(ro)
	defer it.Close()

	it.SeekToLast()
	if it.Valid() {
		return utils.ConvertBytesToUint64(it.Key().Data()), nil
	}
	return 0, nil
}

// GetLog gets a log entry at a given index.
func (db *DB) GetLog(index uint64, log *raft.Log) error {
	val, err := db.Get(utils.ConvertUint64ToBytes(index))
	if err != nil {
		if errors.Is(err, ErrKeyNotFound) {
			return raft.ErrLogNotFound
		}
		return err
	}
	return utils.DecodeMsgPack(val, log)
}

// StoreLog stores a single log entry.
func (db *DB) StoreLog(log *raft.Log) error {
	return db.StoreLogs([]*raft.Log{log})
}

// StoreLogs stores multiple log entries.
func (db *DB) StoreLogs(logs []*raft.Log) error {
	if !db.open.Is() {
		return dberrors.ErrDBNotOpen
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	batch := grocksdb.NewWriteBatch()
	defer batch.Destroy()

	for _, l := range logs {
		key := utils.ConvertUint64ToBytes(l.Index)
		val, err := utils.EncodeMsgPack(l)
		if err != nil {
			return err
		}
		batch.Put(key, val.Bytes())
	}

	wo := grocksdb.NewDefaultWriteOptions()
	defer wo.Destroy()
	return db.db.Write(wo, batch)
}

// DeleteRange deletes a range of log entries.
func (db *DB) DeleteRange(min, max uint64) error {
	if !db.open.Is() {
		return dberrors.ErrDBNotOpen
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	batch := grocksdb.NewWriteBatch()
	defer batch.Destroy()

	for i := min; i <= max; i++ {
		batch.Delete(utils.ConvertUint64ToBytes(i))
	}

	wo := grocksdb.NewDefaultWriteOptions()
	defer wo.Destroy()
	return db.db.Write(wo, batch)
}