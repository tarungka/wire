package store

import (
	"fmt"
	"math/rand"
	"os"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/tarungka/wire/internal/logger"
	"github.com/tarungka/wire/internal/new/db/badgerdb"
	"github.com/tarungka/wire/internal/rsync"
)

// newBenchFSM constructs the minimum NodeStore state required to drive the
// FSM Apply path. It deliberately skips raft transport / snapshot store
// setup since Apply only touches the badgerdb-backed FSM and a few atomics.
//
// BadgerDB is opened on-disk in a tmpdir so the benchmark exercises the
// real fsync-bound write path the production FSM hits.
func newBenchFSM(b *testing.B) (*NodeStore, func()) {
	b.Helper()
	dir, err := os.MkdirTemp("", "fsm-bench-*")
	if err != nil {
		b.Fatalf("mkdtemp: %v", err)
	}
	db := badgerdb.New(&badgerdb.Config{Dir: dir})
	if _, err := db.Open(); err != nil {
		os.RemoveAll(dir)
		b.Fatalf("badger open: %v", err)
	}

	s := &NodeStore{
		db:           db,
		fsmIndex:     &atomic.Uint64{},
		fsmTerm:      &atomic.Uint64{},
		fsmUpdatedAt: rsync.NewAtomicTime(),
		logger:       logger.GetLogger("store-bench"),
	}
	return s, func() {
		db.Close()
		os.RemoveAll(dir)
	}
}

// BenchmarkApply measures the Raft FSM Apply hot path: msgpack-encode a
// raft.Log + persist via BadgerDB Set. Sub-bench by typical payload size.
func BenchmarkApply(b *testing.B) {
	for _, size := range []int{256, 4 * 1024, 64 * 1024} {
		b.Run(fmt.Sprintf("payload=%d", size), func(b *testing.B) {
			s, cleanup := newBenchFSM(b)
			defer cleanup()

			payload := make([]byte, size)
			rand.New(rand.NewSource(int64(size))).Read(payload)
			tmpl := &raft.Log{
				Term: 1,
				Type: raft.LogCommand,
				Data: payload,
			}

			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				tmpl.Index = uint64(i + 1)
				if r := s.Apply(tmpl); r != nil {
					if err, ok := r.(error); ok {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
