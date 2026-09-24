package engine

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// Each fixture entry has an eight-byte key and 1016 pseudorandom value bytes.
// Setup uses bounded atomic batches outside timing; bytes/op counts logical
// key/value payload and excludes index, WAL, manifest and network overhead.
func benchmarkStateFixture(b *testing.B, kind StateBackendType, mib int) (StateBackend, [][]byte, []byte) {
	b.Helper()
	backend, err := NewStateBackend(StateBackendConfig{Type: kind, PebbleDataDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := backend.Close(); err != nil {
			b.Error(err)
		}
	})
	count := mib * 1024
	keys := make([][]byte, count)
	rng := rand.New(rand.NewSource(1))
	batch := make([]StateMutation, 0, 1024)
	for i := 0; i < count; i++ {
		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, uint64(i))
		value := make([]byte, 1016)
		if _, err := rng.Read(value); err != nil {
			b.Fatal(err)
		}
		keys[i] = key
		batch = append(batch, StateMutation{Key: key, Value: value})
		if len(batch) == cap(batch) {
			if err := backend.(BatchedStateBackend).ApplyBatch(batch); err != nil {
				b.Fatal(err)
			}
			batch = batch[:0]
		}
	}
	return backend, keys, make([]byte, 1016)
}

func BenchmarkStateBackendOperations(b *testing.B) {
	for _, kind := range []StateBackendType{StateBackendHashMap, StateBackendPebble} {
		b.Run(string(kind), func(b *testing.B) {
			for _, op := range []string{"PutExisting", "Get", "Iterator"} {
				b.Run(op, func(b *testing.B) {
					backend, keys, value := benchmarkStateFixture(b, kind, 1)
					b.ReportAllocs()
					if op == "Iterator" {
						b.SetBytes(1 << 20)
					} else {
						b.SetBytes(1024)
					}
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						switch op {
						case "PutExisting":
							if err := backend.Put(keys[i%len(keys)], value); err != nil {
								b.Fatal(err)
							}
						case "Get":
							result, err := backend.Get(keys[i%len(keys)])
							if err != nil || len(result) != len(value) {
								b.Fatalf("get: length %d, error %v", len(result), err)
							}
						case "Iterator":
							it := backend.NewIterator(nil)
							count := 0
							for it.Next() {
								count++
								_ = it.Key()
								_ = it.Value()
							}
							it.Close()
							if count != len(keys) {
								b.Fatalf("iterator count = %d", count)
							}
						}
					}
				})
			}
		})
	}
}

func BenchmarkStateBackendCheckpoint(b *testing.B) {
	for _, kind := range []StateBackendType{StateBackendHashMap, StateBackendPebble} {
		for _, mib := range []int{1, 64, 256} {
			b.Run(fmt.Sprintf("%s/%dMiB", kind, mib), func(b *testing.B) {
				backend, _, _ := benchmarkStateFixture(b, kind, mib)
				b.SetBytes(int64(mib) << 20)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					handle, err := backend.Checkpoint(uint64(i + 1))
					if err != nil {
						b.Fatal(err)
					}
					if len(handle.Data) == 0 {
						b.Fatal("empty snapshot")
					}
					// Retention is outside timing and prevents repeated native checkpoints
					// from consuming unbounded disk space during benchmark calibration.
					if kind == StateBackendPebble {
						b.StopTimer()
						var manifest pebbleSnapshotManifest
						if err := json.Unmarshal(handle.Data, &manifest); err != nil {
							b.Fatal(err)
						}
						if err := os.RemoveAll(manifest.Path); err != nil {
							b.Fatal(err)
						}
						b.StartTimer()
					}
				}
			})
		}
	}
}
