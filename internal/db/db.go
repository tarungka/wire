package db

import (
	"expvar"

	"github.com/dgraph-io/badger/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// DBVersion is the BadgerDB version.
var DBVersion string

// stats captures stats for the DB layer.
var stats *expvar.Map

func init() {
	// DBVersion = badger.getVersion()
	stats = expvar.NewMap("db") // TODO: Need to impl this
}

type DB struct {
	dataPath string

	logger *zerolog.Logger
}

// Opens a file-based database at path, if path is empty
// opens it in /tmp/badger
func Open(path string) (*badger.DB, error) {
	if path == "" {
		path = "/tmp/badger"
	}

	db, err := badger.Open(badger.DefaultOptions(path))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("opened a file-based database at %s", path)

	return db, nil
}

// Opens a in memory database
func OpenInMemory() (*badger.DB, error) {
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true))
	if err != nil {
		return nil, err
	}
	log.Debug().Msgf("opened a in-memory database")

	return db, nil
}
