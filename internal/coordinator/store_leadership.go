package coordinator

import (
	"context"
	"errors"
	"sync"
)

// LeadershipStore is a term-scoped handle to the authoritative metadata store.
// Construction does not open Pebble, so a standby can start while the current
// leader owns the database lock. A new handle must be created for each term:
// revoked handles never become writable again, even after a later takeover.
type LeadershipStore struct {
	mu     sync.RWMutex
	ctx    context.Context
	store  MetadataStore
	closed bool
}

// OpenLeadershipStore opens metadata only while the election grant is valid.
// The caller must close this handle before voluntarily releasing its election.
// The factory must open the same authoritative store on every candidate, not
// an independently copied snapshot. Its own exclusive lock fences database I/O.
func OpenLeadershipStore(ctx context.Context, open func() (MetadataStore, error)) (*LeadershipStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	backend, err := open()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, backend.Close())
	}
	return &LeadershipStore{ctx: ctx, store: backend}, nil
}

func (s *LeadershipStore) availableLocked() error {
	if s.closed || s.ctx.Err() != nil {
		return ErrNotLeader
	}
	return nil
}

func (s *LeadershipStore) Get(key []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return nil, err
	}
	return s.store.Get(key)
}

func (s *LeadershipStore) Set(key, value []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return err
	}
	return s.store.Set(key, value)
}

func (s *LeadershipStore) Delete(key []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return err
	}
	return s.store.Delete(key)
}

func (s *LeadershipStore) WriteBatch(batch []KVPair) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return err
	}
	return s.store.WriteBatch(batch)
}

func (s *LeadershipStore) PrefixScan(prefix []byte, fn func([]byte, []byte) bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return err
	}
	err := s.store.PrefixScan(prefix, func(key, value []byte) bool {
		if s.ctx.Err() != nil {
			return false
		}
		return fn(key, value)
	})
	if s.ctx.Err() != nil {
		return errors.Join(ErrNotLeader, err)
	}
	return err
}

func (s *LeadershipStore) Snapshot(destDir string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.availableLocked(); err != nil {
		return err
	}
	return s.store.Snapshot(destDir)
}

// Close waits for operations that were admitted before revocation to finish.
// Storage ownership cannot transfer to another term until this returns.
func (s *LeadershipStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.store.Close()
}
