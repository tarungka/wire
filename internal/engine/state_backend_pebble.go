package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"
)

// PebbleStateBackend keeps each restored database in a separate generation.
// ACTIVE selects the generation to reopen; checkpoints remain independent of it.
// Restore and Close invalidate outstanding iterators.
type PebbleStateBackend struct {
	mu             sync.Mutex
	db             *pebble.DB
	root           string
	activeDir      string
	lock           io.Closer
	iterators      map[*pebbleStateIterator]struct{}
	maxCompactions int
}

func newPebbleStateBackend(cfg StateBackendConfig) (StateBackend, error) {
	maxCompactions := cfg.PebbleMaxCompactionConcurrency
	if maxCompactions < 0 {
		return nil, errors.New("state backend: negative pebble compaction concurrency")
	}
	if maxCompactions == 0 {
		maxCompactions = DefaultPebbleMaxCompactionConcurrency
	}
	if cfg.PebbleDataDir == "" {
		return nil, fmt.Errorf("state backend: pebble requires PebbleDataDir to be set")
	}
	root, err := filepath.Abs(cfg.PebbleDataDir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	lock, err := vfs.Default.Lock(filepath.Join(root, "LOCK"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = lock.Close()
		}
	}()
	data, err := os.ReadFile(filepath.Join(root, "ACTIVE"))
	var name string
	if errors.Is(err, os.ErrNotExist) {
		dir, e := os.MkdirTemp(root, "generation-")
		if e != nil {
			return nil, e
		}
		name = filepath.Base(dir)
	} else if err != nil {
		return nil, err
	} else {
		name = string(data)
		if filepath.Base(name) != name || !strings.HasPrefix(name, "generation-") {
			return nil, fmt.Errorf("invalid state generation %q", name)
		}
	}
	db, err := pebble.Open(filepath.Join(root, name), &pebble.Options{
		ErrorIfNotExists:         len(data) > 0,
		MaxConcurrentCompactions: func() int { return maxCompactions },
	})
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		if _, err = publishStateGeneration(root, name); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	ok = true
	return &PebbleStateBackend{db: db, root: root, activeDir: filepath.Join(root, name), lock: lock, iterators: make(map[*pebbleStateIterator]struct{}), maxCompactions: maxCompactions}, nil
}

func publishStateGeneration(root, name string) (bool, error) {
	// Persist directory entries before making this generation discoverable.
	generation, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return false, err
	}
	err = generation.Sync()
	closeErr := generation.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	f, err := os.CreateTemp(root, "active-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.WriteString(name); err != nil {
		_ = f.Close()
		return false, err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if err = os.Rename(f.Name(), filepath.Join(root, "ACTIVE")); err != nil {
		return false, err
	}
	dir, err := os.Open(root)
	if err != nil {
		return true, err
	}
	defer func() { _ = dir.Close() }()
	return true, dir.Sync()
}

func (b *PebbleStateBackend) Put(key, value []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	return b.db.Set(key, value, pebble.Sync)
}
func (b *PebbleStateBackend) Get(key []byte) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return nil, ErrBackendClosed
	}
	value, closer, err := b.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	result := cloneBytes(value)
	err = closer.Close()
	return result, err
}
func (b *PebbleStateBackend) Delete(key []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	_, closer, err := b.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return ErrKeyNotFound
	}
	if err != nil {
		return err
	}
	if err = closer.Close(); err != nil {
		return err
	}
	return b.db.Delete(key, pebble.Sync)
}
func (b *PebbleStateBackend) NewIterator(prefix []byte) StateIterator {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return &hashMapIterator{}
	}
	upper := cloneBytes(prefix)
	if len(upper) == 0 {
		upper = nil
	} // nil means unbounded; an empty bound excludes every key.
	for i := len(upper) - 1; i >= 0; i-- {
		upper[i]++
		if upper[i] != 0 {
			upper = upper[:i+1]
			break
		}
		if i == 0 {
			upper = nil
		}
	}
	it, err := b.db.NewIter(&pebble.IterOptions{LowerBound: cloneBytes(prefix), UpperBound: upper})
	// StateIterator has no error channel. Fail visibly instead of returning an
	// incomplete state scan that a checkpoint could mistake for success.
	if err != nil {
		panic(fmt.Errorf("state iterator: %w", err))
	}
	result := &pebbleStateIterator{backend: b, iter: it}
	b.iterators[result] = struct{}{}
	return result
}
func (b *PebbleStateBackend) closeIterators() {
	for it := range b.iterators {
		_ = it.iter.Close()
		it.iter = nil
		delete(b.iterators, it)
	}
}
func (b *PebbleStateBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return ErrBackendClosed
	}
	b.closeIterators()
	err := b.db.Close()
	b.db = nil
	return errors.Join(err, b.lock.Close())
}

type pebbleStateIterator struct {
	backend *PebbleStateBackend
	iter    *pebble.Iterator
	started bool
}

func (it *pebbleStateIterator) Next() bool {
	it.backend.mu.Lock()
	defer it.backend.mu.Unlock()
	if it.iter == nil {
		return false
	}
	var valid bool
	if !it.started {
		it.started = true
		valid = it.iter.First()
	} else {
		valid = it.iter.Next()
	}
	if !valid && it.iter.Error() != nil {
		panic(fmt.Errorf("state iterator: %w", it.iter.Error()))
	}
	return valid
}
func (it *pebbleStateIterator) Key() []byte {
	it.backend.mu.Lock()
	defer it.backend.mu.Unlock()
	if it.iter == nil || !it.iter.Valid() {
		return nil
	}
	return cloneBytes(it.iter.Key())
}
func (it *pebbleStateIterator) Value() []byte {
	it.backend.mu.Lock()
	defer it.backend.mu.Unlock()
	if it.iter == nil || !it.iter.Valid() {
		return nil
	}
	return cloneBytes(it.iter.Value())
}
func (it *pebbleStateIterator) Close() {
	it.backend.mu.Lock()
	defer it.backend.mu.Unlock()
	if it.iter != nil {
		_ = it.iter.Close()
		it.iter = nil
		delete(it.backend.iterators, it)
	}
}

// pebbleSnapshotManifest identifies immutable checkpoint files by SHA-256.
type pebbleSnapshotManifest struct {
	Version      int               `json:"version"`
	CheckpointID uint64            `json:"checkpoint_id"`
	Path         string            `json:"path"`
	Files        map[string]string `json:"files"`
}

func (b *PebbleStateBackend) Checkpoint(id uint64) (SnapshotHandle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.db == nil {
		return SnapshotHandle{}, ErrBackendClosed
	}
	path, err := os.MkdirTemp(b.root, "snapshot-")
	if err != nil {
		return SnapshotHandle{}, err
	}
	if err = os.Remove(path); err != nil {
		return SnapshotHandle{}, err
	}
	if err = b.db.Checkpoint(path, pebble.WithFlushedWAL()); err != nil {
		return SnapshotHandle{}, err
	}
	files, err := stateSnapshotHashes(path)
	if err != nil {
		return SnapshotHandle{}, err
	}
	data, err := json.Marshal(pebbleSnapshotManifest{Version: 1, CheckpointID: id, Path: path, Files: files})
	return SnapshotHandle{CheckpointID: id, BackendType: StateBackendPebble, Data: data}, err
}

func openRestoredState(path string, maxCompactions int) (*pebble.DB, error) {
	return pebble.Open(path, &pebble.Options{
		ErrorIfNotExists:         true,
		MaxConcurrentCompactions: func() int { return maxCompactions },
	})
}
