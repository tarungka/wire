package coordinator

import (
	"github.com/cockroachdb/pebble"
)

// PebbleOption is a functional option for configuring the PebbleDB store.
type PebbleOption func(*pebbleConfig)

type pebbleConfig struct {
	memTableSize          int
	l0CompactionThreshold int
	cacheSize             int64
}

func defaultPebbleConfig() pebbleConfig {
	return pebbleConfig{
		memTableSize:          4 << 20, // 4 MB
		l0CompactionThreshold: 4,
		cacheSize:             8 << 20, // 8 MB
	}
}

// WithMemTableSize sets the MemTable size in bytes.
func WithMemTableSize(size int) PebbleOption {
	return func(c *pebbleConfig) { c.memTableSize = size }
}

// WithL0CompactionThreshold sets the L0 compaction trigger threshold.
func WithL0CompactionThreshold(n int) PebbleOption {
	return func(c *pebbleConfig) { c.l0CompactionThreshold = n }
}

// WithCacheSize sets the block cache size in bytes.
func WithCacheSize(size int64) PebbleOption {
	return func(c *pebbleConfig) { c.cacheSize = size }
}

// PebbleStore is a MetadataStore backed by PebbleDB.
type PebbleStore struct {
	db    *pebble.DB
	cache *pebble.Cache
}

// NewPebbleStore opens (or creates) a PebbleDB-backed metadata store at dataDir.
func NewPebbleStore(dataDir string, opts ...PebbleOption) (*PebbleStore, error) {
	cfg := defaultPebbleConfig()
	for _, o := range opts {
		o(&cfg)
	}

	cache := pebble.NewCache(cfg.cacheSize)

	pOpts := &pebble.Options{
		Cache:                    cache,
		MemTableSize:             uint64(cfg.memTableSize),
		L0CompactionThreshold:    cfg.l0CompactionThreshold,
		L0StopWritesThreshold:    12,
		MaxOpenFiles:             500,
		WALDir:                   "", // same as data dir
		DisableWAL:               false,
		MaxConcurrentCompactions: func() int { return 1 },
	}

	db, err := pebble.Open(dataDir, pOpts)
	if err != nil {
		cache.Unref()
		return nil, err
	}

	return &PebbleStore{db: db, cache: cache}, nil
}

func (s *PebbleStore) Get(key []byte) ([]byte, error) {
	val, closer, err := s.db.Get(key)
	if err == pebble.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Copy value before closing.
	result := make([]byte, len(val))
	copy(result, val)
	closer.Close()
	return result, nil
}

func (s *PebbleStore) Set(key, value []byte) error {
	return s.db.Set(key, value, pebble.Sync)
}

func (s *PebbleStore) Delete(key []byte) error {
	return s.db.Delete(key, pebble.Sync)
}

func (s *PebbleStore) WriteBatch(batch []KVPair) error {
	b := s.db.NewBatch()
	for _, kv := range batch {
		if err := b.Set(kv.Key, kv.Value, nil); err != nil {
			b.Close()
			return err
		}
	}
	if err := b.Commit(pebble.Sync); err != nil {
		b.Close()
		return err
	}
	return b.Close()
}

func (s *PebbleStore) PrefixScan(prefix []byte, fn func(key, value []byte) bool) error {
	upper := prefixSuccessor(prefix)
	iterOpts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	}
	iter, err := s.db.NewIter(iterOpts)
	if err != nil {
		return err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if !fn(iter.Key(), iter.Value()) {
			break
		}
	}
	return iter.Error()
}

func (s *PebbleStore) Snapshot(destDir string) error {
	return s.db.Checkpoint(destDir)
}

func (s *PebbleStore) Close() error {
	err := s.db.Close()
	s.cache.Unref()
	return err
}

// prefixSuccessor returns the lexicographically smallest key that is
// greater than all keys with the given prefix. Used as UpperBound for
// PebbleDB iterators.
func prefixSuccessor(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	succ := make([]byte, len(prefix))
	copy(succ, prefix)
	for i := len(succ) - 1; i >= 0; i-- {
		succ[i]++
		if succ[i] != 0 {
			return succ
		}
	}
	return nil // overflow: prefix was all 0xFF
}
