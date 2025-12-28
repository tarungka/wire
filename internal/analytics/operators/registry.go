package operators

import (
	"fmt"
	"sync"

	"github.com/tarungka/wire/internal/analytics"
)

// Factory is a function that creates a new operator instance.
type Factory func(config map[string]interface{}) (analytics.Operator, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)
)

// Register adds a new operator factory to the registry.
func Register(typeName string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[typeName] = factory
}

// Create creates a new operator instance of the given type.
func Create(typeName string, config map[string]interface{}) (analytics.Operator, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	factory, ok := registry[typeName]
	if !ok {
		return nil, fmt.Errorf("unknown operator type: %s", typeName)
	}

	return factory(config)
}
