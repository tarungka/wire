package rpc

import "fmt"

// StateBackendSpec selects storage on the worker. DataDir is a worker-local
// root; runtime adds job/operator/instance/attempt isolation below it.
type StateBackendSpec struct {
	Type                     string `codec:"type"`
	DataDir                  string `codec:"data_dir,omitempty"`
	MaxMemoryBytes           int64  `codec:"max_memory_bytes,omitempty"`
	MaxCompactionConcurrency int    `codec:"max_compaction_concurrency,omitempty"`
}

func (op OperatorDescriptor) ValidateStateBackend() error {
	s := op.StateBackend
	if s == nil {
		return nil
	}
	if op.Type != OperatorTypeProcess && op.Type != OperatorTypeWindow {
		return fmt.Errorf("state backend requires Process or Window operator")
	}
	if s.Type != "" && s.Type != "hashmap" && s.Type != "pebble" {
		return fmt.Errorf("unknown state backend %q", s.Type)
	}
	if s.MaxMemoryBytes < 0 || s.MaxCompactionConcurrency < 0 {
		return fmt.Errorf("state backend limits must be nonnegative")
	}
	return nil
}
