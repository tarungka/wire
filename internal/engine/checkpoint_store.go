package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxStoredCheckpointBytes = 64 * 1024 * 1024
const checkpointFileHeaderSize = 44

var ErrCheckpointConflict = errors.New("checkpoint identity already has different contents")
var ErrCheckpointFileCorrupt = errors.New("checkpoint file corrupt")

// FileCheckpointStore persists inline operator snapshot bytes. Root must be an
// existing, durably created directory owned by the worker. This is the storage
// endpoint for replication, not itself evidence of a remote replica. Referenced
// state-backend files must be transferred separately before acknowledging them.
type FileCheckpointStore struct{ root string }

func NewFileCheckpointStore(root string) (*FileCheckpointStore, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("checkpoint root must be a directory")
	}
	return &FileCheckpointStore{root: root}, nil
}

func (s *FileCheckpointStore) path(jobID, taskID string, id, epoch uint64) (string, error) {
	if jobID == "" || taskID == "" || len(jobID) > 4096 || len(taskID) > 4096 || id == 0 {
		return "", errors.New("invalid checkpoint identity")
	}
	identity, _ := json.Marshal([]any{jobID, taskID, id, epoch})
	digest := sha256.Sum256(identity)
	return filepath.Join(s.root, hex.EncodeToString(digest[:])+".checkpoint"), nil
}

// Put publishes a checksummed, fsynced snapshot atomically without overwriting
// another value for the same identity. Identical retries are idempotent.
func (s *FileCheckpointStore) Put(ctx context.Context, jobID string, snapshot TaskCheckpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination, err := s.path(jobID, snapshot.TaskID, snapshot.CheckpointID, snapshot.EpochID)
	if err != nil {
		return err
	}
	size := uint64(len(snapshot.Operators))*4 + uint64(len(snapshot.Source))
	for _, data := range snapshot.Operators {
		size += uint64(len(data))
	}
	if size > maxStoredCheckpointBytes {
		return errors.New("checkpoint exceeds storage size limit")
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(payload) > maxStoredCheckpointBytes {
		return errors.New("checkpoint exceeds storage size limit")
	}
	digest := sha256.Sum256(payload)
	var header [checkpointFileHeaderSize]byte
	copy(header[:4], "WCP1")
	binary.BigEndian.PutUint64(header[4:12], uint64(len(payload)))
	copy(header[12:], digest[:])
	file, err := os.CreateTemp(s.root, ".checkpoint-pending-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer file.Close()
	if _, err := file.Write(header[:]); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(file.Name(), destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, err := s.read(destination)
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, payload) {
			return ErrCheckpointConflict
		}
	}
	// Remove the staging link before syncing the directory so successful writes
	// persist both publication and cleanup. The deferred remove covers failures.
	if err := os.Remove(file.Name()); err != nil {
		return err
	}
	// Also sync on an identical retry: the earlier publisher may have failed
	// between link and directory fsync, leaving durability unconfirmed.
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *FileCheckpointStore) read(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < checkpointFileHeaderSize || info.Size() > maxStoredCheckpointBytes+checkpointFileHeaderSize {
		return nil, ErrCheckpointFileCorrupt
	}
	var header [checkpointFileHeaderSize]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCheckpointFileCorrupt, err)
	}
	length := binary.BigEndian.Uint64(header[4:12])
	if string(header[:4]) != "WCP1" || length != uint64(info.Size()-checkpointFileHeaderSize) {
		return nil, ErrCheckpointFileCorrupt
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(file, payload); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCheckpointFileCorrupt, err)
	}
	digest := sha256.Sum256(payload)
	if !bytes.Equal(digest[:], header[12:]) {
		return nil, ErrCheckpointFileCorrupt
	}
	return payload, nil
}

func (s *FileCheckpointStore) Get(ctx context.Context, jobID, taskID string, id, epoch uint64) (TaskCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return TaskCheckpoint{}, err
	}
	path, err := s.path(jobID, taskID, id, epoch)
	if err != nil {
		return TaskCheckpoint{}, err
	}
	payload, err := s.read(path)
	if err != nil {
		return TaskCheckpoint{}, err
	}
	var snapshot TaskCheckpoint
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return TaskCheckpoint{}, fmt.Errorf("%w: %v", ErrCheckpointFileCorrupt, err)
	}
	if snapshot.TaskID != taskID || snapshot.CheckpointID != id || snapshot.EpochID != epoch {
		return TaskCheckpoint{}, ErrCheckpointFileCorrupt
	}
	if err := ctx.Err(); err != nil {
		return TaskCheckpoint{}, err
	}
	return snapshot, nil
}
