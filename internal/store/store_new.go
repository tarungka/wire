package store

import (
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/tarungka/wire/pkg/common/rsync"
)

type StoreNew struct {
	open *rsync.AtomicBool

	db            *badger.DB
	snapshotStore badger.DB

	Mu sync.Mutex
}

func NewStore() *StoreNew {
	return &StoreNew{
		open: rsync.NewAtomicBool(),
	}
}
