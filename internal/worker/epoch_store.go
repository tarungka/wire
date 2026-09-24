package worker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var errEpochPersistence = errors.New("worker: cannot persist fencing epoch")

// epochStore is exclusively owned for a worker process's lifetime. The lock is
// on a separate inode because the epoch record is replaced atomically.
type epochStore struct {
	path  string
	lock  *os.File
	epoch uint64
}

func openEpochStore(path string) (*epochStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("epoch path already owned or unavailable: %w", err)
	}
	store := &epochStore{path: path, lock: lock}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		_ = store.close()
		return nil, err
	}
	if len(data) != 8 {
		_ = store.close()
		return nil, fmt.Errorf("invalid worker epoch: expected 8 bytes")
	}
	store.epoch = binary.BigEndian.Uint64(data)
	return store, nil
}

func (s *epochStore) save(epoch uint64) error {
	if epoch < s.epoch {
		return fmt.Errorf("refusing stale epoch %d below %d", epoch, s.epoch)
	}
	if epoch == s.epoch {
		return nil
	}
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, epoch)
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".wire-worker-epoch-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return err
	}
	s.epoch = epoch
	return nil
}

func (s *epochStore) close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}
