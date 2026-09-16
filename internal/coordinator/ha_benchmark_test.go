package coordinator

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tarungka/wire/internal/protocol"
)

func benchmarkHAMetadata(b *testing.B, jobs int) *PebbleStore {
	b.Helper()
	store, err := NewPebbleStore(filepath.Join(b.TempDir(), "metadata"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	batch := make([]KVPair, 0, jobs*2)
	for i := 0; i < jobs; i++ {
		id := fmt.Sprintf("job-%05d", i)
		raw, err := protocol.EncodeMsgPack(JobMeta{ID: id, Name: id, Status: JobRunning, Parallelism: 4, Config: bytes.Repeat([]byte{byte(i)}, 1024), LatestCheckpoint: 1})
		if err != nil {
			b.Fatal(err)
		}
		checkpoint, err := protocol.EncodeMsgPack(CheckpointMeta{ID: 1, JobID: id, EpochID: 1, Status: CheckpointCompleted})
		if err != nil {
			b.Fatal(err)
		}
		batch = append(batch, KVPair{Key: JobMetaKey(id), Value: raw}, KVPair{Key: CheckpointKey(id, 1), Value: checkpoint})
	}
	if err := store.WriteBatch(batch); err != nil {
		b.Fatal(err)
	}
	return store
}

func BenchmarkHARecover10000Jobs(b *testing.B) {
	store := benchmarkHAMetadata(b, 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		began := time.Now()
		state, err := recoverFromStore(store)
		if err != nil {
			b.Fatal(err)
		}
		if len(state.jobs) != 10000 || len(state.latestCheckpoints) != 10000 {
			b.Fatal("incomplete recovery")
		}
		if time.Since(began) >= 5*time.Second {
			b.Fatal("10,000-job recovery exceeded WIP's five-second target")
		}
	}
}

func BenchmarkHASnapshot1000Jobs(b *testing.B) {
	store := benchmarkHAMetadata(b, 1000)
	root := b.TempDir()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dest := filepath.Join(root, fmt.Sprintf("snapshot-%d", i))
		if err := store.Snapshot(dest); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		snapshot, err := NewPebbleStore(dest)
		if err != nil {
			b.Fatal(err)
		}
		count := 0
		err = snapshot.PrefixScan([]byte(JobsPrefix), func(key, value []byte) bool {
			expected, readErr := store.Get(key)
			if readErr != nil || !bytes.Equal(expected, value) {
				b.Errorf("snapshot differs at %q", key)
				return false
			}
			count++
			return true
		})
		if closeErr := snapshot.Close(); closeErr != nil {
			b.Fatal(closeErr)
		}
		if err != nil || count != 2000 {
			b.Fatalf("incomplete snapshot: %d keys, %v", count, err)
		}
		b.StartTimer()
	}
}

func BenchmarkHADurableMetadataWrite(b *testing.B) {
	store := benchmarkHAMetadata(b, 1)
	value := bytes.Repeat([]byte("x"), 1024)
	b.SetBytes(int64(len(value)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Set([]byte("benchmark"), value); err != nil {
			b.Fatal(err)
		}
	}
}
