package badgerdb

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/hashicorp/raft"
)

// Benchmarks default to in-memory BadgerDB to keep numbers reproducible.
// Set BENCH_DISK=1 to run against an on-disk tmpdir instead, which is
// fsync-bound and dominated by storage latency.
func newBenchDB(b *testing.B) (*DB, func()) {
	b.Helper()

	if os.Getenv("BENCH_DISK") == "1" {
		dir, err := os.MkdirTemp("", "badger-bench-*")
		if err != nil {
			b.Fatalf("mkdtemp: %v", err)
		}
		db := New(&Config{Dir: dir})
		bdb, err := db.Open()
		if err != nil {
			os.RemoveAll(dir)
			b.Fatalf("open: %v", err)
		}
		db.db = bdb
		return db, func() { bdb.Close(); os.RemoveAll(dir) }
	}

	db := New(&Config{})
	bdb, err := db.OpenInMemory()
	if err != nil {
		b.Fatalf("open in-memory: %v", err)
	}
	db.db = bdb
	db.open.Set()
	return db, func() { bdb.Close() }
}

func BenchmarkSet(b *testing.B) {
	for _, size := range []int{64, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			db, cleanup := newBenchDB(b)
			defer cleanup()

			val := make([]byte, size)
			rand.New(rand.NewSource(1)).Read(val)
			key := make([]byte, 16)

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				binary.BigEndian.PutUint64(key, uint64(i))
				if err := db.Set(key, val); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGet(b *testing.B) {
	for _, size := range []int{64, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			db, cleanup := newBenchDB(b)
			defer cleanup()

			const populated = 10_000
			val := make([]byte, size)
			rand.New(rand.NewSource(2)).Read(val)
			key := make([]byte, 16)
			for i := 0; i < populated; i++ {
				binary.BigEndian.PutUint64(key, uint64(i))
				if err := db.Set(key, val); err != nil {
					b.Fatalf("populate: %v", err)
				}
			}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				binary.BigEndian.PutUint64(key, uint64(i%populated))
				if _, err := db.Get(key); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSetUint64(b *testing.B) {
	db, cleanup := newBenchDB(b)
	defer cleanup()
	key := make([]byte, 16)

	b.ReportAllocs()
	b.SetBytes(8)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(key, uint64(i))
		if err := db.SetUint64(key, uint64(i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetUint64(b *testing.B) {
	db, cleanup := newBenchDB(b)
	defer cleanup()
	const populated = 10_000
	key := make([]byte, 16)
	for i := 0; i < populated; i++ {
		binary.BigEndian.PutUint64(key, uint64(i))
		if err := db.SetUint64(key, uint64(i)); err != nil {
			b.Fatalf("populate: %v", err)
		}
	}

	b.ReportAllocs()
	b.SetBytes(8)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binary.BigEndian.PutUint64(key, uint64(i%populated))
		if _, err := db.GetUint64(key); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreLogs(b *testing.B) {
	for _, batch := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("batch=%d", batch), func(b *testing.B) {
			db, cleanup := newBenchDB(b)
			defer cleanup()

			payload := make([]byte, 256)
			rand.New(rand.NewSource(3)).Read(payload)
			logs := make([]*raft.Log, batch)
			var idx uint64 = 1

			b.ReportAllocs()
			b.SetBytes(int64(batch * (len(payload) + 16)))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				for j := 0; j < batch; j++ {
					logs[j] = &raft.Log{
						Index: idx,
						Term:  1,
						Type:  raft.LogCommand,
						Data:  payload,
					}
					idx++
				}
				if err := db.StoreLogs(logs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
