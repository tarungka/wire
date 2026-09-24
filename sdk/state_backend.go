package sdk

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/tarungka/wire/internal/engine"
	"github.com/tarungka/wire/internal/rpc"
)

// StateBackendConfig selects storage for each Process/window instance.
// An explicit Pebble directory is retained across executions; an omitted one
// uses a temporary directory removed when execution closes the operator.
type StateBackendConfig struct {
	Type        string
	DataDir     string
	MaxMemoryMB int
	// MaxCompactionConcurrency bounds Pebble compactions per operator instance.
	// Zero selects two. It has no effect on the hashmap backend.
	MaxCompactionConcurrency int
}

// NewHashMapStateBackend selects bounded in-memory state (0 means unlimited).
// Each parallel operator instance receives its own backend and limit.
func NewHashMapStateBackend(maxMemoryMB int) StateBackendConfig {
	return StateBackendConfig{Type: "hashmap", MaxMemoryMB: maxMemoryMB}
}

// NewPebbleStateBackend selects disk state rooted at dataDir. This config is
// a factory specification, not a shared database handle.
func NewPebbleStateBackend(dataDir string) StateBackendConfig {
	return StateBackendConfig{Type: "pebble", DataDir: dataDir}
}

// SetStateBackend selects storage for keyed Process and window operators.
// In Cluster mode DataDir is a worker-local root, isolated by job/operator/instance.
func (env *StreamExecutionEnvironment) SetStateBackend(config StateBackendConfig) *StreamExecutionEnvironment {
	env.stateBackend = config
	env.stateBackendSet = true
	return env
}

func (c StateBackendConfig) validate() error {
	if c.MaxCompactionConcurrency < 0 {
		return fmt.Errorf("%w: negative compaction concurrency", ErrInvalidConfig)
	}
	if c.Type != "" && c.Type != "pebble" && c.Type != "hashmap" {
		return fmt.Errorf("%w: unknown state backend %q", ErrInvalidConfig, c.Type)
	}
	if c.MaxMemoryMB < 0 || int64(c.MaxMemoryMB) > math.MaxInt64/(1024*1024) {
		return fmt.Errorf("%w: invalid state memory limit", ErrInvalidConfig)
	}
	return nil
}
func (c StateBackendConfig) open(nodeID, instance int) (engine.StateBackend, func(), error) {
	cleanup := func() {}
	cfg := engine.StateBackendConfig{Type: engine.StateBackendType(c.Type), HashMapMemLimit: int64(c.MaxMemoryMB) * 1024 * 1024, PebbleMaxCompactionConcurrency: c.MaxCompactionConcurrency}
	if cfg.Type == "" || cfg.Type == engine.StateBackendPebble {
		if c.DataDir == "" {
			dir, err := os.MkdirTemp("", "wire-state-")
			if err != nil {
				return nil, cleanup, err
			}
			cfg.PebbleDataDir = dir
			cleanup = func() { _ = os.RemoveAll(dir) }
		} else {
			cfg.PebbleDataDir = filepath.Join(c.DataDir, fmt.Sprintf("operator-%d", nodeID), fmt.Sprintf("instance-%d", instance))
		}
	}
	backend, err := engine.NewStateBackend(cfg)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return backend, cleanup, nil
}

func (env *StreamExecutionEnvironment) configureGraphStateBackend(graph *rpc.JobGraph) {
	if !env.stateBackendSet {
		return
	}
	c := env.stateBackend
	for i := range graph.Operators {
		op := &graph.Operators[i]
		if op.Type == rpc.OperatorTypeProcess || op.Type == rpc.OperatorTypeWindow {
			op.StateBackend = &rpc.StateBackendSpec{Type: c.Type, DataDir: c.DataDir, MaxMemoryBytes: int64(c.MaxMemoryMB) * 1024 * 1024, MaxCompactionConcurrency: c.MaxCompactionConcurrency}
		}
	}
}
