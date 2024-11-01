package badgerdb

import (
	"github.com/dgraph-io/badger/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

func (db *DB) Open(path string) (*badger.DB, error) {
	if path == "" {
		path = "/tmp/badger"
	}

	badgerDB, err := badger.Open(badger.DefaultOptions(path))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("opened a file-based database at %s", path)

	return badgerDB, nil
}

// Opens a in memory database
func (db *DB) OpenInMemory() (*badger.DB, error) {
	badgerInMemory, err := badger.Open(badger.DefaultOptions("").WithInMemory(true))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("opened a in-memory database")

	return badgerInMemory, nil
}
