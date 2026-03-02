package engine

import "fmt"

// Default state backend configuration values per WIP-18.
const (
	// DefaultStateBackendType is PebbleDB for production workloads.
	DefaultStateBackendType = StateBackendPebble

	// DefaultHashMapMemLimit is the default logical payload size limit for the
	// HashMap state backend (sum of key + value bytes). 0 means unlimited
	// (useful for testing). Does not account for Go runtime overhead.
	DefaultHashMapMemLimit int64 = 0
)

// StateBackendConfig holds configuration for state backend creation.
type StateBackendConfig struct {
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
}

// DefaultStateBackendConfig returns a StateBackendConfig with default values.
func DefaultStateBackendConfig() StateBackendConfig {
	return StateBackendConfig{
		Type:            DefaultStateBackendType,
		HashMapMemLimit: DefaultHashMapMemLimit,
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
		return NewHashMapStateBackend(cfg.HashMapMemLimit), nil
	case StateBackendPebble:
		return newPebbleStateBackend(cfg)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownBackendType, backendType)
	}
}

// newPebbleStateBackend creates a PebbleDB-backed state backend.
// This is a placeholder that will be fully implemented when the engine's
// Pebble integration is wired up. The coordinator package already has a
// working PebbleStore implementation (internal/coordinator/store_pebble.go)
// that demonstrates the pattern.
func newPebbleStateBackend(cfg StateBackendConfig) (StateBackend, error) {
	if cfg.PebbleDataDir == "" {
		return nil, fmt.Errorf("state backend: pebble requires PebbleDataDir to be set")
	}
	// TODO(WIP-18): Wire up PebbleDB state backend using the same pattern as
	// internal/coordinator/store_pebble.go. For now, return an error
	// indicating the backend is not yet available for direct engine use.
	return nil, fmt.Errorf("state backend: pebble backend is not yet available for direct engine use (use hashmap for testing)")
}
