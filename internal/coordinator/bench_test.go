package coordinator

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"testing"
)

// silentPebbleLogger satisfies pebble.Logger and drops everything. Used
// only in benchmarks so WAL-replay / compaction info logs don't pollute
// the bench output stream.
type silentPebbleLogger struct{}

func (silentPebbleLogger) Infof(string, ...interface{}) {}
func (silentPebbleLogger) Fatalf(format string, args ...interface{}) {
	log.Fatalf(format, args...)
}

// newPebbleBench opens a fresh PebbleStore on a tmpdir for a benchmark and
// returns a cleanup func. PebbleDB is fsync-bound on Set/WriteBatch, so
// these numbers reflect the real production write path.
func newPebbleBench(b *testing.B) (*PebbleStore, func()) {
	b.Helper()
	dir := b.TempDir()
	s, err := NewPebbleStore(dir, WithLogger(silentPebbleLogger{}))
	if err != nil {
		b.Fatalf("NewPebbleStore: %v", err)
	}
	return s, func() { _ = s.Close() }
}

// BenchmarkPebbleStore_Set is the metadata-write hot path. PebbleDB is
// fsync'd on every Set, so this number is dominated by disk.
func BenchmarkPebbleStore_Set(b *testing.B) {
	for _, size := range []int{64, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("value=%dB", size), func(b *testing.B) {
			s, cleanup := newPebbleBench(b)
			defer cleanup()

			val := make([]byte, size)
			rand.New(rand.NewSource(1)).Read(val)
			key := make([]byte, 16)

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				binary.BigEndian.PutUint64(key, uint64(i))
				if err := s.Set(key, val); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPebbleStore_Get reads after pre-populating the store. The block
// cache is warm so this is a CPU+memory-copy bench, not a disk bench.
//
// Population uses WriteBatch instead of N×Set because pebble.Sync fsyncs
// per Set; populating 10K keys serially would take >60s and trip the
// default `go test -timeout 10m`.
func BenchmarkPebbleStore_Get(b *testing.B) {
	for _, size := range []int{64, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("value=%dB", size), func(b *testing.B) {
			s, cleanup := newPebbleBench(b)
			defer cleanup()

			const populated = 10_000
			val := make([]byte, size)
			rand.New(rand.NewSource(2)).Read(val)
			batch := make([]KVPair, populated)
			for i := 0; i < populated; i++ {
				k := make([]byte, 16)
				binary.BigEndian.PutUint64(k, uint64(i))
				batch[i] = KVPair{Key: k, Value: val}
			}
			if err := s.WriteBatch(batch); err != nil {
				b.Fatalf("populate: %v", err)
			}
			key := make([]byte, 16)

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				binary.BigEndian.PutUint64(key, uint64(i%populated))
				if _, err := s.Get(key); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPebbleStore_WriteBatch measures atomic batch writes. This is the
// path the coordinator takes when transitioning a job (multiple keys touched
// in one fsync).
func BenchmarkPebbleStore_WriteBatch(b *testing.B) {
	for _, batch := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			s, cleanup := newPebbleBench(b)
			defer cleanup()

			payload := make([]byte, 256)
			rand.New(rand.NewSource(3)).Read(payload)
			pairs := make([]KVPair, batch)
			var idx uint64

			b.ReportAllocs()
			b.SetBytes(int64(batch * (len(payload) + 16)))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					key := make([]byte, 16)
					binary.BigEndian.PutUint64(key, idx)
					pairs[j] = KVPair{Key: key, Value: payload}
					idx++
				}
				if err := s.WriteBatch(pairs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMemoryStore_Set / Get / WriteBatch are the upper-bound numbers
// without any storage layer. The gap to PebbleStore is your fsync cost.
func BenchmarkMemoryStore_Set(b *testing.B) {
	s := NewMemoryStore()
	val := make([]byte, 256)
	key := make([]byte, 16)

	b.ReportAllocs()
	b.SetBytes(int64(len(val)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(key, uint64(i))
		if err := s.Set(key, val); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryStore_Get(b *testing.B) {
	s := NewMemoryStore()
	val := make([]byte, 256)
	const populated = 10_000
	key := make([]byte, 16)
	for i := 0; i < populated; i++ {
		binary.BigEndian.PutUint64(key, uint64(i))
		if err := s.Set(key, val); err != nil {
			b.Fatalf("populate: %v", err)
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(val)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(key, uint64(i%populated))
		if _, err := s.Get(key); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryStore_WriteBatch(b *testing.B) {
	for _, batch := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			s := NewMemoryStore()
			payload := make([]byte, 256)
			pairs := make([]KVPair, batch)
			var idx uint64

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					key := make([]byte, 16)
					binary.BigEndian.PutUint64(key, idx)
					pairs[j] = KVPair{Key: key, Value: payload}
					idx++
				}
				if err := s.WriteBatch(pairs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPebbleStore_Delete measures the per-key delete cost. Like Set,
// each Delete fsyncs (pebble.Sync), so this is fsync-bound.
func BenchmarkPebbleStore_Delete(b *testing.B) {
	s, cleanup := newPebbleBench(b)
	defer cleanup()

	// Pre-populate via WriteBatch so setup doesn't dominate the bench.
	const populated = 5_000
	val := make([]byte, 64)
	batch := make([]KVPair, populated)
	for i := 0; i < populated; i++ {
		k := make([]byte, 16)
		binary.BigEndian.PutUint64(k, uint64(i))
		batch[i] = KVPair{Key: k, Value: val}
	}
	if err := s.WriteBatch(batch); err != nil {
		b.Fatalf("populate: %v", err)
	}
	key := make([]byte, 16)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(key, uint64(i%populated))
		if err := s.Delete(key); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPebbleStore_PrefixScan iterates jobs/* keys with a callback.
// This is the path the coordinator uses for `GET /api/v1/jobs` and similar
// listings. Time should grow with N keys under the prefix.
func BenchmarkPebbleStore_PrefixScan(b *testing.B) {
	for _, n := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			s, cleanup := newPebbleBench(b)
			defer cleanup()

			val := make([]byte, 256)
			batch := make([]KVPair, n)
			for i := 0; i < n; i++ {
				batch[i] = KVPair{Key: JobMetaKey(fmt.Sprintf("job-%06d", i)), Value: val}
			}
			if err := s.WriteBatch(batch); err != nil {
				b.Fatalf("populate: %v", err)
			}
			prefix := []byte("jobs/")

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				count := 0
				if err := s.PrefixScan(prefix, func(_, _ []byte) bool {
					count++
					return true
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPebbleStore_Open measures cold-start latency: opening a store
// directory that already has SSTables on disk. This is the recovery path.
func BenchmarkPebbleStore_Open(b *testing.B) {
	dir := b.TempDir()

	// Seed the store with some data, then close it so the next Open does
	// real work (LSM scan, manifest replay).
	s, err := NewPebbleStore(dir, WithLogger(silentPebbleLogger{}))
	if err != nil {
		b.Fatal(err)
	}
	val := make([]byte, 256)
	batch := make([]KVPair, 1000)
	for i := 0; i < 1000; i++ {
		k := make([]byte, 16)
		binary.BigEndian.PutUint64(k, uint64(i))
		batch[i] = KVPair{Key: k, Value: val}
	}
	if err := s.WriteBatch(batch); err != nil {
		b.Fatal(err)
	}
	_ = s.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s, err := NewPebbleStore(dir, WithLogger(silentPebbleLogger{}))
		if err != nil {
			b.Fatal(err)
		}
		_ = s.Close()
	}
}

// BenchmarkMemoryStore_PrefixScan is the no-fsync reference for PrefixScan.
// MemoryStore uses a sorted slice, so scan cost is O(n) regardless of
// prefix size — useful for spotting algorithmic regressions.
func BenchmarkMemoryStore_PrefixScan(b *testing.B) {
	for _, n := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("keys=%d", n), func(b *testing.B) {
			s := NewMemoryStore()
			val := make([]byte, 256)
			batch := make([]KVPair, n)
			for i := 0; i < n; i++ {
				batch[i] = KVPair{Key: JobMetaKey(fmt.Sprintf("job-%06d", i)), Value: val}
			}
			if err := s.WriteBatch(batch); err != nil {
				b.Fatalf("populate: %v", err)
			}
			prefix := []byte("jobs/")

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				count := 0
				if err := s.PrefixScan(prefix, func(_, _ []byte) bool {
					count++
					return true
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkValidateTransition measures the pure state-machine validation
// cost (no I/O, no locks). Called on every job state transition.
func BenchmarkValidateTransition(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateTransition(JobRunning, JobFinishing)
	}
}

// BenchmarkKeyConstruction times the per-call allocation cost of the key
// helpers in keys.go. Each call goes through fmt.Appendf which allocates
// a fresh []byte per invocation; that adds up on hot paths.
func BenchmarkKeyConstruction(b *testing.B) {
	b.Run("JobMetaKey", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = JobMetaKey("job-12345-67890")
		}
	})
	b.Run("CheckpointKey", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = CheckpointKey("job-12345-67890", uint64(i))
		}
	})
	b.Run("WorkerMetaKey", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = WorkerMetaKey("worker-abc-123")
		}
	})
}
