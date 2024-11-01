package rocksdb

import (
	"github.com/rs/zerolog"
	"github.com/tarungka/wire/internal/logger"
)

type Config struct {
	Dir string
}

type DB struct {
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
