package engine

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// ScopedStateBackendFactory opens fresh backend handles for one task instance.
// Explicit roots are retained. Temporary state is removed after operator Close.
func ScopedStateBackendFactory(config StateBackendConfig, jobID, operatorID, attemptID string, instance int) func() (StateBackend, func(), error) {
	return func() (StateBackend, func(), error) {
		cfg := config
		if cfg.MetricTaskID != "" {
			cfg.MetricOperatorID = operatorID
		}
		cleanup := func() {}
		if cfg.Type == "" || cfg.Type == StateBackendPebble {
			if cfg.PebbleDataDir == "" {
				dir, err := os.MkdirTemp("", "wire-worker-state-")
				if err != nil {
					return nil, cleanup, err
				}
				cfg.PebbleDataDir = dir
				cleanup = func() { _ = os.RemoveAll(dir) }
			} else {
				identity := sha256.Sum256([]byte(jobID + "\x00" + operatorID))
				attempt := sha256.Sum256([]byte(attemptID))
				cfg.PebbleDataDir = filepath.Join(cfg.PebbleDataDir, fmt.Sprintf("%x", identity), fmt.Sprintf("instance-%d", instance), fmt.Sprintf("attempt-%x", attempt))
			}
		}
		backend, err := NewStateBackend(cfg)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		return backend, cleanup, nil
	}
}
