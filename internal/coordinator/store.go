package coordinator

// KVPair represents a key-value pair for batch writes.
type KVPair struct {
	Key   []byte
	Value []byte
}

// MetadataStore is the interface for persistent coordinator metadata storage.
// All implementations must be safe for concurrent use by multiple goroutines.
type MetadataStore interface {
	// Get retrieves the value for a key. Returns nil, nil if the key does not exist.
	Get(key []byte) ([]byte, error)

	// Set stores a key-value pair. Overwrites any existing value.
	Set(key, value []byte) error

	// Delete removes a key. No error if the key does not exist.
	Delete(key []byte) error

	// WriteBatch atomically writes a batch of key-value pairs.
	WriteBatch(batch []KVPair) error

	// PrefixScan iterates over all keys with the given prefix in sorted order.
	// The callback receives each key-value pair. Return false to stop iteration.
	PrefixScan(prefix []byte, fn func(key, value []byte) bool) error

	// Snapshot creates a point-in-time snapshot of the store at destDir.
	Snapshot(destDir string) error

	// Close releases all resources held by the store.
	Close() error
}
