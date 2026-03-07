package sdk

// ProcessContext provides access to keyed state during stateful processing.
type ProcessContext interface {
	// Key returns the current event's key.
	Key() []byte
	// GetValueState returns a ValueState scoped to the current key.
	GetValueState(name string) ValueState
	// GetListState returns a ListState scoped to the current key.
	GetListState(name string) ListState
	// GetMapState returns a MapState scoped to the current key.
	GetMapState(name string) MapState
}

// ValueState stores a single value per key.
type ValueState interface {
	// Get returns the current value, or nil if not set.
	Get() []byte
	// Set updates the stored value.
	Set(value []byte)
	// Clear removes the stored value.
	Clear()
}

// ListState stores an ordered list of values per key.
type ListState interface {
	// Get returns all values in the list.
	Get() [][]byte
	// Add appends a value to the list.
	Add(value []byte)
	// Clear removes all values.
	Clear()
}

// MapState stores key-value pairs per key.
type MapState interface {
	// Get returns the value for the given map key, or nil if not present.
	Get(key string) []byte
	// Put stores a value for the given map key.
	Put(key string, value []byte)
	// Delete removes the given map key.
	Delete(key string)
	// Keys returns all map keys.
	Keys() []string
	// Clear removes all entries.
	Clear()
}
