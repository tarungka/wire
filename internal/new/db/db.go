package db

import (
	"errors"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/rqlite/raft-boltdb/v2"
	"github.com/tarungka/wire/internal/new/db/badgerdb"
	"github.com/tarungka/wire/internal/new/db/rocksdb"
	"go.etcd.io/bbolt"
)

type Config struct {
	Dir string
}

type DbStore interface {
	// Stable store functions
	Get(key []byte) ([]byte, error)

	Set(key, val []byte) error

	SetUint64(key []byte, val uint64) error

	GetUint64(key []byte) (uint64, error)

	// Log store functions
	FirstIndex() (uint64, error)

	LastIndex() (uint64, error)

	GetLog(index uint64, log *raft.Log) error

	StoreLog(log *raft.Log) error

	StoreLogs(logs []*raft.Log) error

	DeleteRange(min, max uint64) error

	Close() error
}

func New(dbType string, config *Config) (DbStore, error) {
	switch dbType {
	case "badgerdb":
		db := badgerdb.New((*badgerdb.Config)(config))
		db.Open()
		return db, nil
	case "rocksdb":
		return rocksdb.New((*rocksdb.Config)(config)), nil
	case "bbolt":
		bs, err := raftboltdb.New(raftboltdb.Options{
			BoltOptions: &bbolt.Options{
				NoFreelistSync: false,
			},
			Path: "/tmp/bbolt-store",
		})
		if err != nil {
			return nil, err
		}
		return bs, nil
	default:
		return nil, errors.New("error unsupported database type")
	}
}
