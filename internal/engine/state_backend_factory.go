package engine

import (
	"fmt"

	"github.com/tarungka/wire/internal/observability"
)

// Default state backend configuration values per WIP-18.
const (
	// DefaultStateBackendType is PebbleDB for production workloads.
	DefaultStateBackendType = StateBackendPebble

	DefaultPebbleMaxCompactionConcurrency = 2

	// DefaultHashMapMemLimit is the default logical payload size limit for the
	// HashMap state backend (sum of key + value bytes). 0 means unlimited
	// (useful for testing). Does not account for Go runtime overhead.
	DefaultHashMapMemLimit int64 = 0
)

// StateBackendConfig holds configuration for state backend creation.
type StateBackendConfig struct {
	// MetricTaskID and MetricOperatorID identify an active managed backend.
	// Empty identity disables per-task metrics for staging and standalone stores.
	MetricTaskID, MetricOperatorID string
	// Type selects the state backend implementation.
	// Defaults to StateBackendPebble if empty.
	Type StateBackendType

	// HashMapMemLimit sets the logical payload size limit for the HashMap
	// backend in bytes (sum of key + value bytes, excluding Go runtime overhead).
	// 0 = unlimited. Ignored for other backend types.
	HashMapMemLimit int64

	// PebbleDataDir is the directory for PebbleDB data files.
	// Required when Type == StateBackendPebble.
	PebbleDataDir string

	// PebbleMaxCompactionConcurrency bounds background compactions per backend.
	// Zero selects the default of two. Negative values are invalid.
	PebbleMaxCompactionConcurrency int
}

// DefaultStateBackendConfig returns a StateBackendConfig with default values.
func DefaultStateBackendConfig() StateBackendConfig {
	return StateBackendConfig{
		Type:                           DefaultStateBackendType,
		HashMapMemLimit:                DefaultHashMapMemLimit,
		PebbleMaxCompactionConcurrency: DefaultPebbleMaxCompactionConcurrency,
	}
}

// NewStateBackend creates a StateBackend based on the given configuration.
// Returns ErrUnknownBackendType for unrecognized backend types.
func NewStateBackend(cfg StateBackendConfig) (StateBackend, error) {
	backendType := cfg.Type
	if backendType == "" {
		backendType = DefaultStateBackendType
	}

	switch backendType {
	case StateBackendHashMap:
		backend := NewHashMapStateBackend(cfg.HashMapMemLimit)
		if cfg.MetricTaskID != "" || cfg.MetricOperatorID != "" {
			recorder, err := observability.NewStateBackendRecorder(cfg.MetricOperatorID, cfg.MetricTaskID, backend.MemUsage)
			if err != nil {
				_ = backend.Close()
				return nil, err
			}
			backend.metrics = recorder
		}
		return backend, nil
	case StateBackendPebble:
		return newPebbleStateBackend(cfg)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownBackendType, backendType)
	}
}
