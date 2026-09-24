package sdk

import "time"

// ProcessContext provides access to keyed state during stateful processing.
type ProcessContext interface {
	CurrentKey() []byte
	GetState(name string) ValueState
	CurrentEventTime() int64
	CurrentWatermark() int64
	RegisterEventTimeTimer(timestamp int64)
	DeleteEventTimeTimer(timestamp int64)
	EmitToSideOutput(tag OutputTag, event Event)
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
	Value() ([]byte, error)
	ValueInt64() (int64, error)
	ValueFloat64() (float64, error)
	ValueString() (string, error)
	SetInt64(int64) error
	SetFloat64(float64) error
	SetString(string) error
	WithTTL(time.Duration) ValueState
	// Get returns the current value, or nil if not set.
	Get() []byte
	// Set updates the stored value.
	Set(value []byte)
	// Clear removes the stored value.
	Clear()
}

// ListState stores an ordered list of values per key.
type ListState interface {
	WithTTL(time.Duration) ListState
	// Get returns all values in the list.
	Get() [][]byte
	// Add appends a value to the list.
	Add(value []byte)
	// Clear removes all values.
	Clear()
}

// MapState stores key-value pairs per key.
type MapState interface {
	WithTTL(time.Duration) MapState
	Entries() (map[string][]byte, error)
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
