# Wire — Performance Baseline

This is the reference baseline for `feat/bench-main` at commit `8df9b37`. Re-capture
after every hot-path change with `make bench-save` and compare with `benchstat`.

## Environment

| Field | Value |
|---|---|
| Date | 2026-05-09 |
| Branch / Commit | `feat/bench-main` / `8df9b37` |
| Go | `go1.24.0 linux/amd64` |
| OS | Linux 6.8.0-111-generic |
| CPU | AMD Ryzen 7 5700X 8-Core (16 logical) |
| PebbleDB | on-disk tmpdir (real fsync) |
| FrameStream | localhost loopback TCP + yamux |
| Run | `make bench` → `go test -bench=. -benchmem -benchtime=3s -count=3 -timeout=30m ./...` |
| Wall-time | ~14 min total |
| Raw output | [`runs/20260509-132632.txt`](runs/20260509-132632.txt) |

## Headline findings

These are the bottlenecks that jumped out the first time the suite was run. Each one
has a concrete file/function pointer and a one-line "what to try first."

1. **PebbleDB Set is fsync-bound at ~6 ms/op.** That's a hard ceiling of **~167
   single-threaded writes/sec.** The number is identical at 64 B, 1 KB, and 16 KB —
   confirming the time is in `pebble.Sync`'s fdatasync, not in payload size.
   `internal/coordinator/store_pebble.go:90` writes with `pebble.Sync`. Every
   metadata mutation pays a full disk round-trip. **What to try:** use `WriteBatch`
   for any path that touches >1 key in one operation — `BenchmarkPebbleStore_WriteBatch/batch=256`
   shows 4.7 ms total for 256 keys, **256× cheaper per key**. Alternatively, expose
   a `pebble.NoSync` variant for non-leader replicas where the WAL-is-truth invariant
   already holds.
2. **`BarrierAligner_BufferDrain` allocates 531 KB / 109 allocs per drain.** Per call,
   not per record. `internal/engine/bench_test.go:BenchmarkBarrierAligner_BufferDrain`.
   For a 100 Hz checkpoint cadence that's ~50 MB/s of churn just from drain. **What
   to try:** reuse the buffered-events slice across drains, or move it behind a
   `sync.Pool`.
3. **`OperatorChain_MapPassthrough` is 384 ns/op + 3 allocs per record.** That sets
   a streaming ceiling around **2.5 M records/sec per chain**, before any user code
   runs. `OperatorChain_FlatMap` is 644 ns + 6 allocs (almost 2× because of the
   per-output allocation). `OperatorChain_Sink` is the fastest, 158 ns + 1 alloc.
   **What to try:** identify which 3 allocations the Map path takes per record (envelope
   creation? metric emit?) and pool the largest one.
4. **`ReadRPCFrame` allocates a fresh payload buffer per call.** 3–4 allocs per
   read, regardless of payload size. `internal/rpc/codec.go:95` does
   `payload = make([]byte, payloadLen)` inside the read loop. The write path uses
   `rpcFramePool` (zero allocs) — read should mirror it. **What to try:** size-class
   the read buffer pool the same way as the write pool.
5. **`FrameStream_WriteRead` round-trip is ~70 µs over loopback yamux.** That's the
   floor for a single inter-node message latency on a healthy cluster network. At
   16 KB it climbs to 90 µs — so payload size adds about 1.5 µs/KB. **Implication:**
   anything called more than ~14 K times/sec across nodes will be dominated by this
   overhead — batch where possible.
6. **`KeyConstruction` (`fmt.Appendf`) costs 130 ns + 2 allocs per key.**
   `internal/coordinator/keys.go` builds keys via `fmt.Appendf(nil, "jobs/%s/meta", id)`.
   For something on the metadata write path, that's wasteful. **What to try:**
   pre-allocated byte builders (`bytes.NewBuffer` + `WriteString`) cut both the
   alloc count and the format-parse cost.

## Headline numbers

These are the per-package summaries in benchstat-style format. Three iterations per
bench (`-count=3`) — values shown are the median of the three. `B/op` and
`allocs/op` are heap pressure per call; `MB/s` is the bench's own `b.SetBytes`
throughput where set.

```
goos: linux
goarch: amd64
cpu: AMD Ryzen 7 5700X 8-Core Processor

# Coordinator — PebbleDB metadata store (fsync-bound writes, fast cache reads)
BenchmarkPebbleStore_Set/value=64B-16              5,983,376 ns/op       0.01 MB/s     76 B/op    0 allocs/op
BenchmarkPebbleStore_Set/value=1024B-16            6,131,057 ns/op       0.17 MB/s    273 B/op    0 allocs/op
BenchmarkPebbleStore_Set/value=16384B-16           6,349,395 ns/op       2.58 MB/s   1587 B/op    1 allocs/op
BenchmarkPebbleStore_Get/value=64B-16                  480.1 ns/op     133.30 MB/s    240 B/op    2 allocs/op
BenchmarkPebbleStore_Get/value=1024B-16              2,353   ns/op     435.24 MB/s   1091 B/op    2 allocs/op
BenchmarkPebbleStore_Get/value=16384B-16             6,664   ns/op    2458.57 MB/s  16489 B/op    2 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=1-16         6,016,782 ns/op       0.05 MB/s     91 B/op    1 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=16-16        6,182,510 ns/op       0.70 MB/s    956 B/op   16 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=256-16       4,763,899 ns/op      14.62 MB/s  10335 B/op  261 allocs/op

# Coordinator — MemoryStore (the no-fsync upper bound — gap to Pebble = your fsync cost)
BenchmarkMemoryStore_Set-16                            414.7 ns/op     617.25 MB/s    519 B/op    2 allocs/op
BenchmarkMemoryStore_Get-16                            192.0 ns/op    1333.64 MB/s    256 B/op    1 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=1-16             497.0 ns/op                    515 B/op    3 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=16-16          7,195   ns/op                   8022 B/op   48 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=256-16       110,483   ns/op                 133273 B/op  768 allocs/op

# Coordinator — pure helpers
BenchmarkValidateTransition-16                          23.86 ns/op                     0 B/op    0 allocs/op
BenchmarkKeyConstruction/JobMetaKey-16                 126.3 ns/op                    48 B/op    2 allocs/op
BenchmarkKeyConstruction/CheckpointKey-16              215.4 ns/op                    88 B/op    3 allocs/op
BenchmarkKeyConstruction/WorkerMetaKey-16              131.5 ns/op                    48 B/op    2 allocs/op

# Engine — operator chain (per-record streaming cost)
BenchmarkOperatorChain_MapPassthrough-16               384.8 ns/op                   133 B/op    3 allocs/op
BenchmarkOperatorChain_FlatMap-16                      636.7 ns/op                   365 B/op    6 allocs/op
BenchmarkOperatorChain_Sink-16                         162.3 ns/op                     5 B/op    1 allocs/op
BenchmarkOperatorChain_MapPassthrough_WithErrorHandler-16  395.8 ns/op               133 B/op    3 allocs/op
BenchmarkBarrierAligner_BufferDrain-16              82,076   ns/op                531585 B/op  109 allocs/op

# Keygroup — partition assignment (murmur3, no allocs)
BenchmarkKeyGroup_128-16                                10.44 ns/op                     0 B/op    0 allocs/op
BenchmarkKeyGroup_32768-16                              10.49 ns/op                     0 B/op    0 allocs/op
BenchmarkAssignedTask-16                                 0.29 ns/op                     0 B/op    0 allocs/op
BenchmarkEncodePebbleKey-16                             25.72 ns/op                    32 B/op    1 allocs/op
BenchmarkDecodePebbleKey-16                              3.0  ns/op                     0 B/op    0 allocs/op

# Protocol — wire frame I/O (msgpack + CRC32C)
BenchmarkWriteFrame_DataRecord_1KB-16               1,201   ns/op                  3461 B/op    8 allocs/op
BenchmarkReadFrame_DataRecord_1KB-16                  322.7 ns/op                  1209 B/op    4 allocs/op
BenchmarkWriteFrame_CheckpointBarrier-16              484.7 ns/op                   448 B/op    7 allocs/op
BenchmarkEncodeMsgPack_DataRecord-16                1,104   ns/op                  3445 B/op    6 allocs/op
BenchmarkDecodeMsgPack_DataRecord-16                  611.5 ns/op                  1443 B/op    5 allocs/op
BenchmarkCRC32C_1KB-16                                 52.67 ns/op  19443.01 MB/s     0 B/op    0 allocs/op
BenchmarkCRC32C_16MB-16                           748,803   ns/op  22405.37 MB/s     0 B/op    0 allocs/op
BenchmarkReadFrame_Concurrent-16                      255.9 ns/op                  1210 B/op    4 allocs/op
BenchmarkCRC32C_Concurrent-16                         198.7 ns/op   5153.25 MB/s    48 B/op    1 allocs/op

# RPC — codec + dispatch
BenchmarkWriteRPCFrame/payload=empty-16                26.89 ns/op    446.28 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=64B-16                  31.35 ns/op   2424.56 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=1KB-16                  51.03 ns/op  20303.15 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=16KB-16                573.2 ns/op  28604.01 MB/s     0 B/op    0 allocs/op
BenchmarkReadRPCFrame/payload=empty-16                 87.03 ns/op    137.88 MB/s    64 B/op    3 allocs/op
BenchmarkReadRPCFrame/payload=64B-16                  128.8 ns/op    590.27 MB/s   128 B/op    4 allocs/op
BenchmarkReadRPCFrame/payload=1KB-16                  301.0 ns/op   3442.04 MB/s  1088 B/op    4 allocs/op
BenchmarkReadRPCFrame/payload=16KB-16               2,058   ns/op   7966.97 MB/s 16448 B/op    4 allocs/op
BenchmarkEncodeRPCRequest-16                          887.3 ns/op                   449 B/op    5 allocs/op
BenchmarkDecodeRPCPayload-16                          422.5 ns/op                   456 B/op    4 allocs/op
BenchmarkMethodName-16                                  2.57 ns/op                     0 B/op    0 allocs/op

# Transport — yamux frame stream over localhost loopback
BenchmarkFrameStream_WriteRead/size=64B-16          69,758   ns/op       0.92 MB/s   1240 B/op   19 allocs/op
BenchmarkFrameStream_WriteRead/size=1KB-16          65,815   ns/op      15.56 MB/s   6259 B/op   20 allocs/op
BenchmarkFrameStream_WriteRead/size=16KB-16         89,787   ns/op     182.48 MB/s 101809 B/op   24 allocs/op
BenchmarkFrameStream_StreamSetup-16                124,164   ns/op                  7434 B/op   75 allocs/op
```

## How to read these numbers

`ns/op` is wall time per call, `B/op` is heap bytes allocated per call, `allocs/op`
is the number of separate heap objects per call. The `MB/s` column comes from
`b.SetBytes(N)` and only matters when the bench is data-throughput-bound.

Three buckets:
- **"Just how fast it is"** — protocol marshal, RPC codec, keygroup hashing. You
  can't make these dramatically faster without a rewrite.
- **"Real bug, fix it"** — `pebble.Sync` on every Set, `ReadRPCFrame` not pooling,
  `BarrierAligner_BufferDrain` allocating half a MB. One-line wins live here.
- **"Architectural cost"** — `OperatorChain_*` per-record allocs, fsync floor on
  PebbleDB. These need design changes to move the needle.

## Notes for re-running

- Full suite walltime is ~14 minutes at `-benchtime=3s -count=3` because PebbleDB
  Set/WriteBatch are fsync-bound at ~6 ms/op (each iteration of those benches
  produces ~600 ops in 3 s, then ×3 count, ×9 sub-benches).
- `make bench` filters BadgerDB/yamux log noise via `grep --line-buffered` so
  `bench.out` only contains parseable lines (suitable for `benchstat` directly).
- `make bench-save` runs at `-count=5` instead of `3` and archives output under
  `docs/benchmarks/runs/<timestamp>.txt`.

## How to run a single bench

```sh
make bench BENCH=BenchmarkPebbleStore_Set
make bench BENCH_PKGS=./internal/engine/...
make bench BENCH=BenchmarkOperatorChain BENCH_PKGS=./internal/engine/...
```

## How to capture a CPU profile

```sh
# Profile the bench suite for 10s and open the flame graph in a browser
make profile-cpu BENCH=BenchmarkPebbleStore_Set
go tool pprof -http=:6060 cpu.prof

# Memory profile (allocations)
make profile-mem BENCH=BenchmarkBarrierAligner_BufferDrain

# Runtime/execution trace (goroutines, GC, scheduler)
make trace
go tool trace trace.out
```

## Live profiling against a running coordinator

The coordinator's HTTP server now exposes the standard `/debug/pprof/*` endpoints
on its API port (default `:4001`).

```sh
# 30-second CPU flame graph from the live coordinator
make profile-live DURATION=30 PPROF_HOST=http://localhost:4001

# Heap snapshot
go tool pprof -http=:6060 http://localhost:4001/debug/pprof/heap

# Mutex / block contention — run wire with --profile-extra to enable
go tool pprof -http=:6060 http://localhost:4001/debug/pprof/mutex
go tool pprof -http=:6060 http://localhost:4001/debug/pprof/block

# Live execution trace
curl -o trace.out 'http://localhost:4001/debug/trace?seconds=10'
go tool trace trace.out
```

## Statistical comparison after a change

```sh
make bench-save                                 # before
# ... change code ...
make bench-save                                 # after
benchstat docs/benchmarks/runs/<old>.txt docs/benchmarks/runs/<new>.txt
```

If you don't already have benchstat: `go install golang.org/x/perf/cmd/benchstat@latest`
