package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// ValidateCheckpointFiles verifies a complete manifest against a checkpoint
// directory. os.Root keeps opens contained even when a path changes concurrently.
// Symlinks are rejected as non-portable checkpoint entries, including links that
// currently point inside the root. Callers must use contained opens for subsequent
// reads too; validation does not make later arbitrary path-based reads safe.
func ValidateCheckpointFiles(ctx context.Context, directory string, metadata *CheckpointMetadata) error {
	if err := metadata.ValidateComplete(); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, task := range metadata.Tasks {
		prefix := ""
		for _, component := range strings.Split(strings.TrimSuffix(task.StatePath, "/"), "/") {
			prefix = path.Join(prefix, component)
			info, err := root.Lstat(prefix)
			if err != nil {
				return fmt.Errorf("task %s directory: %w", task.TaskID, err)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("task %s state directory is not a regular directory", task.TaskID)
			}
		}
		var size int64
		for _, name := range task.StateFiles {
			if err := ctx.Err(); err != nil {
				return err
			}
			filePath := path.Join(prefix, name)
			info, err := root.Lstat(filePath)
			if err != nil {
				return fmt.Errorf("task %s state file: %w", task.TaskID, err)
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("task %s has non-regular state file %s", task.TaskID, name)
			}
			file, err := root.Open(filePath)
			if err != nil {
				return err
			}
			// Count actual bytes rather than trusting a stale size from Lstat. Bound
			// each read by the remaining manifest size to reject growing files.
			remaining := task.StateSizeBytes - size
			digest := sha256.New()
			n, readErr := io.Copy(digest, io.LimitReader(snapshotContextReader{ctx: ctx, reader: file}, remaining))
			var extra [1]byte
			extraN, extraErr := file.Read(extra[:])
			closeErr := file.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			if extraN != 0 || extraErr != io.EOF {
				return fmt.Errorf("task %s state exceeds manifest size", task.TaskID)
			}
			if expected := task.StateSHA256[name]; expected != "" && hex.EncodeToString(digest.Sum(nil)) != expected {
				return fmt.Errorf("task %s state checksum mismatch for %s", task.TaskID, name)
			}
			size += n
		}
		if size != task.StateSizeBytes {
			return fmt.Errorf("task %s state size mismatch: got %d, expected %d", task.TaskID, size, task.StateSizeBytes)
		}
	}
	return ctx.Err()
}
