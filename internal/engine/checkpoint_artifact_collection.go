package engine

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CollectArtifacts removes unreferenced published Pebble imports. artifactRoot
// must be owned exclusively by this worker's checkpoint store. Callers must fence
// recovery references before deleting records; a returned handle alone is not a
// live reference. In-flight imports are protected by the shared root gate.
func (s *FileCheckpointStore) CollectArtifacts(ctx context.Context, artifactRoot string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.artifactsMu.Lock()
	defer s.artifactsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := filepath.Abs(artifactRoot)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, err
	}
	referenced := make(map[string]bool)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if !strings.HasSuffix(entry.Name(), ".checkpoint") {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		if err := checkpointNotDeleted(path); errors.Is(err, ErrCheckpointDeleted) {
			continue
		} else if err != nil {
			return 0, err
		}
		if !entry.Type().IsRegular() {
			return 0, ErrCheckpointFileCorrupt
		}
		raw, err := s.read(path)
		if err != nil {
			return 0, err
		}
		var snapshot TaskCheckpoint
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return 0, err
		}
		if err := snapshot.ValidateStateHandles(); err != nil {
			return 0, err
		}
		for _, index := range snapshot.StateHandleIndexes {
			data := snapshot.Source
			if index >= 0 {
				data = snapshot.Operators[index]
			}
			var handle SnapshotHandle
			if err := json.Unmarshal(data, &handle); err != nil {
				return 0, err
			}
			if handle.BackendType != StateBackendPebble {
				continue
			}
			var manifest pebbleSnapshotManifest
			if err := json.Unmarshal(handle.Data, &manifest); err != nil {
				return 0, err
			}
			path, err := filepath.Abs(manifest.Path)
			if err != nil {
				return 0, err
			}
			if filepath.Dir(path) != root || !isImportedArtifactName(filepath.Base(path)) {
				return 0, fmt.Errorf("checkpoint artifact outside owned import root")
			}
			referenced[filepath.Base(path)] = true
		}
	}
	artifacts, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !isImportedArtifactName(artifact.Name()) || referenced[artifact.Name()] {
			continue
		}
		if !artifact.IsDir() || artifact.Type()&os.ModeSymlink != 0 {
			return removed, fmt.Errorf("non-directory checkpoint artifact")
		}
		if err := os.RemoveAll(filepath.Join(root, artifact.Name())); err != nil {
			return removed, err
		}
		removed++
	}
	directory, err := os.Open(root)
	if err != nil {
		return removed, err
	}
	defer directory.Close()
	return removed, directory.Sync()
}

func isImportedArtifactName(name string) bool {
	if !strings.HasPrefix(name, "snapshot-") || len(name) != len("snapshot-")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, "snapshot-"))
	return err == nil
}
