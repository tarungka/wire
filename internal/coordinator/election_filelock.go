package coordinator

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// FileLockElection implements leader election using file locking (flock).
// Suitable for development and single-host multi-process deployments.
type FileLockElection struct {
	mu       sync.Mutex
	lockPath string
	addr     string
	nodeID   string
	lockFile *os.File
	lctx     *LeaderContext
}

// NewFileLockElection creates a new file-lock election backend.
// lockPath is the path to the lock file. addr is this node's HTTP address.
func NewFileLockElection(lockPath, addr string) *FileLockElection {
	return &FileLockElection{
		lockPath: lockPath,
		addr:     addr,
	}
}

func (f *FileLockElection) Campaign(ctx context.Context, nodeID string) (*LeaderContext, error) {
	f.mu.Lock()
	f.nodeID = nodeID
	f.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(f.lockPath), 0o755); err != nil {
		return nil, err
	}

	// Retry with backoff until we acquire the lock.
	backoff := 100 * time.Millisecond
	maxBackoff := 2 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		lockFile, err := os.OpenFile(f.lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, err
		}

		// Try non-blocking exclusive lock.
		err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			_ = lockFile.Close()
			if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
				return nil, fmt.Errorf("acquire election lock: %w", err)
			}
			// Lock held by another process, wait and retry.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}

		// Lock acquired. Read and increment epoch from companion file.
		epoch, err := f.incrementEpoch()
		if err != nil {
			_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
			_ = lockFile.Close()
			return nil, err
		}

		lctx, cancel := context.WithCancel(ctx)
		f.mu.Lock()
		f.lockFile = lockFile
		f.lctx = &LeaderContext{
			Epoch:  epoch,
			Ctx:    lctx,
			Cancel: cancel,
		}
		f.mu.Unlock()

		return f.lctx, nil
	}
}

func (f *FileLockElection) Resign(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.lctx != nil {
		f.lctx.Cancel()
		f.lctx = nil
	}
	if f.lockFile != nil {
		_ = syscall.Flock(int(f.lockFile.Fd()), syscall.LOCK_UN)
		_ = f.lockFile.Close()
		f.lockFile = nil
	}
	return nil
}

func (f *FileLockElection) GetLeader(_ context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.lctx == nil {
		return "", "", ErrNoLeader
	}
	return f.nodeID, f.addr, nil
}

func (f *FileLockElection) Close() error {
	return f.Resign(context.Background())
}

// incrementEpoch reads the epoch from a companion file, increments it,
// and writes it back. The companion file is lockPath + ".epoch".
func (f *FileLockElection) incrementEpoch() (uint64, error) {
	epochPath := f.lockPath + ".epoch"
	var epoch uint64

	data, err := os.ReadFile(epochPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("read election epoch: %w", err)
	}
	if err == nil {
		if len(data) != 8 {
			return 0, fmt.Errorf("%w: election epoch must contain exactly 8 bytes", ErrStoreCorrupted)
		}
		epoch = binary.BigEndian.Uint64(data)
	}
	if epoch == math.MaxUint64 {
		return 0, fmt.Errorf("%w: election epoch exhausted", ErrRecoveryFailed)
	}
	epoch++

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, epoch)
	if err := writeDurableElectionFile(epochPath, buf); err != nil {
		return 0, err
	}
	return epoch, nil
}

// Replace and sync the companion record while holding the election lock. Never
// truncate the only fencing token in place: a crash could otherwise reuse it.
func writeDurableElectionFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".wire-election-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
