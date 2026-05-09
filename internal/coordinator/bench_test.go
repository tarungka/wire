package coordinator

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// newPebbleBench opens a fresh PebbleStore on a tmpdir for a benchmark and
// returns a cleanup func. PebbleDB is fsync-bound on Set/WriteBatch, so
// these numbers reflect the real production write path.
func newPebbleBench(b *testing.B) (*PebbleStore, func()) {
	b.Helper()
	dir := b.TempDir()
	s, err := NewPebbleStore(dir)
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
