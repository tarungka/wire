package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointManifestFiles(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "truncated", "corrupt", "symlink", "directory symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			m := completeManifest()
			for i := range m.Tasks {
				task := &m.Tasks[i]
				task.StateFiles = []string{"state"}
				task.StateSizeBytes = 3
				digest := sha256.Sum256([]byte("abc"))
				task.StateSHA256 = map[string]string{"state": hex.EncodeToString(digest[:])}
				if err := os.Mkdir(filepath.Join(root, task.StatePath), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, task.StatePath, "state"), []byte("abc"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(root, m.Tasks[0].StatePath, "state")
			switch mode {
			case "missing":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(target, []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "truncated":
				if err := os.WriteFile(target, []byte("a"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, m.Tasks[1].StatePath, "state"), target); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				dir := filepath.Dir(target)
				if err := os.Rename(dir, dir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dir+"-old", dir); err != nil {
					t.Fatal(err)
				}
			}
			err := ValidateCheckpointFiles(context.Background(), root, m)
			if mode == "valid" && err != nil {
				t.Fatal(err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
}
