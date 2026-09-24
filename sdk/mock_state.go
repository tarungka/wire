package sdk

import (
	"math"

	"github.com/tarungka/wire/internal/engine"
)

// NewMockProcessContext uses the same managed-state implementation as runtime,
// backed by an in-memory store. For timer callbacks use NewProcessHarness.
func NewMockProcessContext(key []byte) ProcessContext {
	return &backendProcessContext{key: append([]byte(nil), key...), backend: engine.NewHashMapStateBackend(0), watermark: math.MinInt64}
}
