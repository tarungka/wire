package coordinator

import (
	"context"
	"encoding/binary"
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

		lctx, cancel := context.WithCancel(context.Background())
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
	if err == nil && len(data) == 8 {
		epoch = binary.BigEndian.Uint64(data)
	}
	epoch++

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, epoch)
	if err := os.WriteFile(epochPath, buf, 0o644); err != nil {
		return 0, err
	}
	return epoch, nil
}
