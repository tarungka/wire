# Wire — Performance Baseline

Reference baseline for `feat/bench-main`. Re-capture with `make bench-save`
after every hot-path change and compare with `benchstat`.

## Environment

| Field | Value |
|---|---|
| Date | 2026-05-09 |
| Branch / Commit | `feat/bench-main` / `8df9b37` |
| Go | `go1.24.0 linux/amd64` |
| OS | Linux 6.8.0-111-generic |
| CPU | AMD Ryzen 7 5700X 8-Core (16 logical) |
| PebbleDB | on-disk tmpdir, default cache (8 MB), real fsync |
| FrameStream | localhost loopback TCP + yamux |
| Run | `make bench` → `go test -bench=. -benchmem -benchtime=3s -count=3 -timeout=30m ./...` |
| Wall-time | ~18 min total |
| Coverage | **77 unique benchmarks** across 7 packages |
| Raw output | [`runs/20260509-140524.txt`](runs/20260509-140524.txt) |

## Headline findings

The bottlenecks that jumped out the first time. Each one has a concrete
file/function pointer and a one-line "what to try first."

1. **PebbleDB Set / Delete / WriteBatch are fsync-bound at ~6.5 ms regardless
   of payload or batch size.** A single Set is ~167 ops/sec single-threaded;
   batching 256 keys into one `WriteBatch` is **256× cheaper per key** because
   they share one fsync. `internal/coordinator/store_pebble.go:90` always
   passes `pebble.Sync`. **What to try:** prefer `WriteBatch` on every multi-
   key path (state transitions, recovery), and consider a `pebble.NoSync`
   variant for non-leader replicas where the WAL invariant already holds.
2. **`UnmarshalCheckpointMetadata` is the recovery-path bottleneck — 2.3 ms
   and 2,347 allocations for a 256-task checkpoint.** Encode is 4 allocations
   total (`json.MarshalIndent` is one `bytes.Buffer` plus result); decode does
   ~9 allocations *per task*. The Go stdlib JSON decoder allocates a separate
   `interface{}` for every leaf. `internal/engine/checkpoint_metadata.go:160`.
   **What to try:** swap to `encoding/json/v2` if available, or `goccy/go-json`
   — both regularly cut decode allocs by 5-10×.
3. **`PebbleStore_Open` cold-start is 48.7 ms + 395 KB / 683 allocations.**
   This runs once per coordinator boot and is on the recovery latency budget
   — a leader failover can't ack writes until Open returns. **What to try:**
   nothing immediate; just be aware the budget exists. Tracking it in a
   bench prevents accidental regression from changes to compaction or LSM
   defaults.
4. **`BarrierAligner_BufferDrain` allocates 531 KB and 109 allocations per
   drain.** Per checkpoint, not per record. At 100 Hz checkpoint cadence
   that's ~50 MB/s of churn just from drain. `internal/engine/`.
   **What to try:** reuse the buffered-events slice across drains (or pool
   it) — easy win.
5. **`OperatorChain_MapPassthrough` is 367 ns/op + 3 allocs per record.**
   Caps streaming throughput around **~2.7 M records/sec per chain** before
   any user code runs. FlatMap is 605 ns + 6 allocs (almost 2× because of
   the per-output allocation). Sink is the cheapest at 158 ns + 1 alloc.
6. **`ReadRPCFrame` allocates a fresh payload buffer per call.**
   `internal/rpc/codec.go:95` does `payload = make([]byte, payloadLen)` inside
   the read loop. The write path uses `rpcFramePool` (zero allocs). **What to
   try:** size-class the read buffer pool the same way as the write pool.
7. **Full RPC round-trip floor: 33.6 µs in-memory, ~70 µs over yamux+TCP loopback.**
   `BenchmarkClientCall_Heartbeat` is 33.6 µs (net.Pipe), `BenchmarkFrameStream_WriteRead`
   is ~70 µs at 1 KB. The ~37 µs gap is yamux multiplexing + TCP socket overhead.
   **TLS adds another ~16 µs** (`BenchmarkFrameStream_WriteRead_TLS` = 85.9 µs).
8. **`KeyConstruction` (`fmt.Appendf`) costs 125-225 ns + 2-3 allocs per key.**
   `internal/coordinator/keys.go`. For something on the metadata write path,
   that's wasteful. **What to try:** pre-allocated byte builders cut both
   alloc count and format-parse cost. `CheckpointPath` family in engine has
   the same shape (200 ns + 3 allocs).
9. **`MemoryStore_PrefixScan` allocates per key in the callback.**
   1,000 allocations for 1,000 keys. The PebbleStore equivalent does 2 allocs
   total. **What to try:** the callback signature passes mutable slice headers
   that the scan implementation appears to copy each time. Audit
   `internal/coordinator/store_memory.go:106`.

## Headline numbers

Per-bench median of 3 iterations. `B/op` is heap bytes per call,
`allocs/op` is heap object count, `MB/s` is `b.SetBytes` throughput where
applicable.

```
goos: linux
goarch: amd64
cpu: AMD Ryzen 7 5700X 8-Core Processor

# Coordinator — PebbleDB metadata store (fsync-bound writes, fast cache reads)
BenchmarkPebbleStore_Set/value=64B-16              6,768,589 ns/op       0.01 MB/s     82 B/op    0 allocs/op
BenchmarkPebbleStore_Set/value=1024B-16            6,751,789 ns/op       0.15 MB/s    300 B/op    0 allocs/op
BenchmarkPebbleStore_Set/value=16384B-16           7,006,243 ns/op       2.34 MB/s   1624 B/op    1 allocs/op
BenchmarkPebbleStore_Get/value=64B-16                  542.4 ns/op     117.98 MB/s    240 B/op    2 allocs/op
BenchmarkPebbleStore_Get/value=1024B-16              2,598   ns/op     394.19 MB/s   1092 B/op    2 allocs/op
BenchmarkPebbleStore_Get/value=16384B-16             6,506   ns/op    2518.27 MB/s  16504 B/op    2 allocs/op
BenchmarkPebbleStore_Delete-16                     6,270,699 ns/op                     1 B/op    0 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=1-16         6,789,533 ns/op       0.04 MB/s     99 B/op    1 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=16-16        6,475,158 ns/op       0.67 MB/s    995 B/op   16 allocs/op
BenchmarkPebbleStore_WriteBatch/batch=256-16       5,312,157 ns/op      13.11 MB/s  10248 B/op  261 allocs/op
BenchmarkPebbleStore_PrefixScan/keys=10-16             1,315 ns/op                    13 B/op    2 allocs/op
BenchmarkPebbleStore_PrefixScan/keys=100-16            6,306 ns/op                    13 B/op    2 allocs/op
BenchmarkPebbleStore_PrefixScan/keys=1000-16          58,610 ns/op                    21 B/op    2 allocs/op
BenchmarkPebbleStore_Open-16                      48,711,803 ns/op                396795 B/op  686 allocs/op

# Coordinator — MemoryStore (no-fsync upper bound; gap to PebbleStore = your fsync cost)
BenchmarkMemoryStore_Set-16                              404.0 ns/op     633.61 MB/s    478 B/op    2 allocs/op
BenchmarkMemoryStore_Get-16                              190.6 ns/op    1342.82 MB/s    256 B/op    1 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=1-16               457.0 ns/op                    490 B/op    3 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=16-16            7,402   ns/op                   7899 B/op   48 allocs/op
BenchmarkMemoryStore_WriteBatch/batch=256-16         106,586   ns/op                 125275 B/op  768 allocs/op
BenchmarkMemoryStore_PrefixScan/keys=10-16               388.6 ns/op                    240 B/op   10 allocs/op
BenchmarkMemoryStore_PrefixScan/keys=100-16            3,629   ns/op                   2400 B/op  100 allocs/op
BenchmarkMemoryStore_PrefixScan/keys=1000-16          35,587   ns/op                  24000 B/op 1000 allocs/op

# Coordinator — pure helpers
BenchmarkValidateTransition-16                           23.71 ns/op                     0 B/op    0 allocs/op
BenchmarkKeyConstruction/JobMetaKey-16                  125.0 ns/op                    48 B/op    2 allocs/op
BenchmarkKeyConstruction/CheckpointKey-16               225.2 ns/op                    88 B/op    3 allocs/op
BenchmarkKeyConstruction/WorkerMetaKey-16               131.3 ns/op                    48 B/op    2 allocs/op

# Engine — operator chain (per-record streaming cost)
BenchmarkOperatorChain_MapPassthrough-16                367.0 ns/op                   133 B/op    3 allocs/op
BenchmarkOperatorChain_FlatMap-16                       605.3 ns/op                   365 B/op    6 allocs/op
BenchmarkOperatorChain_Sink-16                          157.8 ns/op                     5 B/op    1 allocs/op
BenchmarkOperatorChain_MapPassthrough_WithErrorHandler-16  392.1 ns/op                133 B/op    3 allocs/op
BenchmarkBarrierAligner_BufferDrain-16              107,870   ns/op                531584 B/op  109 allocs/op

# Engine — checkpoint metadata I/O (recovery + checkpoint completion paths)
BenchmarkMarshalCheckpointMetadata/tasks=1-16         7,099   ns/op    111.98 MB/s   1827 B/op    4 allocs/op
BenchmarkMarshalCheckpointMetadata/tasks=16-16       52,829   ns/op    138.99 MB/s  14501 B/op    4 allocs/op
BenchmarkMarshalCheckpointMetadata/tasks=256-16     787,089   ns/op    144.69 MB/s 228357 B/op    4 allocs/op
BenchmarkUnmarshalCheckpointMetadata/tasks=1-16      17,508   ns/op     45.41 MB/s   1200 B/op   30 allocs/op
BenchmarkUnmarshalCheckpointMetadata/tasks=16-16    153,822   ns/op     47.74 MB/s  11184 B/op  179 allocs/op
BenchmarkUnmarshalCheckpointMetadata/tasks=256-16 2,293,379   ns/op     49.66 MB/s 183379 B/op 2347 allocs/op
BenchmarkValidateCheckpointMetadata-16                7,100   ns/op                   6992 B/op    6 allocs/op
BenchmarkCheckpointPath/CheckpointPath-16               200.8 ns/op                    88 B/op    3 allocs/op
BenchmarkCheckpointPath/SavepointPath-16                199.4 ns/op                    88 B/op    3 allocs/op
BenchmarkCheckpointPath/CheckpointDir-16                188.8 ns/op                    72 B/op    3 allocs/op
BenchmarkCheckpointPath/TaskStatePath-16                114.8 ns/op                    32 B/op    2 allocs/op

# Keygroup — partition assignment (murmur3, no allocs)
BenchmarkKeyGroup_128-16                                 11.41 ns/op                     0 B/op    0 allocs/op
BenchmarkKeyGroup_32768-16                               11.30 ns/op                     0 B/op    0 allocs/op
BenchmarkAssignedTask-16                                  0.32 ns/op                     0 B/op    0 allocs/op
BenchmarkEncodePebbleKey-16                              30.47 ns/op                    32 B/op    1 allocs/op
BenchmarkDecodePebbleKey-16                               3.00 ns/op                     0 B/op    0 allocs/op

# Protocol — wire frame I/O (msgpack + CRC32C)
BenchmarkWriteFrame_DataRecord_1KB-16                 1,404   ns/op                  3461 B/op    8 allocs/op
BenchmarkReadFrame_DataRecord_1KB-16                    372.8 ns/op                  1209 B/op    4 allocs/op
BenchmarkWriteFrame_CheckpointBarrier-16                545.8 ns/op                   448 B/op    7 allocs/op
BenchmarkEncodeMsgPack_DataRecord-16                  1,255   ns/op                  3445 B/op    6 allocs/op
BenchmarkDecodeMsgPack_DataRecord-16                    666.1 ns/op                  1443 B/op    5 allocs/op
BenchmarkCRC32C_1KB-16                                   55.00 ns/op  18617.86 MB/s     0 B/op    0 allocs/op
BenchmarkCRC32C_16MB-16                           1,319,958   ns/op  12710.42 MB/s     0 B/op    0 allocs/op
BenchmarkReadFrame_Concurrent-16                        288.6 ns/op                  1210 B/op    4 allocs/op
BenchmarkCRC32C_Concurrent-16                           224.6 ns/op   4558.44 MB/s    48 B/op    1 allocs/op

# RPC — codec, dispatch, full round-trip
BenchmarkWriteRPCFrame/payload=empty-16                  26.30 ns/op    456.36 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=64B-16                    30.60 ns/op   2483.80 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=1KB-16                    52.82 ns/op  19615.14 MB/s     0 B/op    0 allocs/op
BenchmarkWriteRPCFrame/payload=16KB-16                  570.7 ns/op  28728.23 MB/s     0 B/op    0 allocs/op
BenchmarkReadRPCFrame/payload=empty-16                   83.18 ns/op    144.27 MB/s    64 B/op    3 allocs/op
BenchmarkReadRPCFrame/payload=64B-16                    125.6 ns/op    605.26 MB/s   128 B/op    4 allocs/op
BenchmarkReadRPCFrame/payload=1KB-16                    292.7 ns/op   3539.75 MB/s  1088 B/op    4 allocs/op
BenchmarkReadRPCFrame/payload=16KB-16                 3,103   ns/op   5284.39 MB/s 16448 B/op    4 allocs/op
BenchmarkEncodeRPCRequest-16                          1,021   ns/op                   449 B/op    5 allocs/op
BenchmarkDecodeRPCPayload-16                            514.8 ns/op                   456 B/op    4 allocs/op
BenchmarkMethodName-16                                    2.98 ns/op                     0 B/op    0 allocs/op
BenchmarkClientCall_Heartbeat-16                     33,605   ns/op                  7912 B/op   75 allocs/op

# Transport — yamux frame stream (loopback TCP)
BenchmarkFrameStream_WriteRead/size=64B-16           76,331   ns/op       0.84 MB/s   1239 B/op   19 allocs/op
BenchmarkFrameStream_WriteRead/size=1KB-16           69,481   ns/op      14.74 MB/s   6259 B/op   20 allocs/op
BenchmarkFrameStream_WriteRead/size=16KB-16         123,739   ns/op     132.41 MB/s 101829 B/op   24 allocs/op
BenchmarkFrameStream_WriteRead_TLS-16                85,901   ns/op      11.92 MB/s   6367 B/op   23 allocs/op
BenchmarkFrameStream_StreamSetup-16                 142,052   ns/op                  7392 B/op   74 allocs/op

# Worker — registry build (per-task setup)
BenchmarkRegistry_Build/Source-16                        26.08 ns/op                     0 B/op    0 allocs/op
BenchmarkRegistry_Build/Map-16                           26.51 ns/op                     0 B/op    0 allocs/op
BenchmarkRegistry_Build/FlatMap-16                       27.89 ns/op                     0 B/op    0 allocs/op
BenchmarkRegistry_Build/Sink-16                          26.46 ns/op                     0 B/op    0 allocs/op
```

## What's covered now

| Layer | Benches | What they bound |
|---|---|---|
| Coordinator (Pebble) | 14 | metadata write/read/scan/open ceiling — fsync-bound writes, in-cache reads, recovery cold-start |
| Coordinator (Memory) | 8 | no-fsync upper bound for the same ops; gap = real fsync cost |
| Coordinator (helpers) | 4 | state-machine validation + key construction overhead |
| Engine (operator chain) | 5 | per-record streaming throughput ceiling |
| Engine (checkpoint) | 11 | metadata serde + path builders on the recovery path |
| Keygroup | 5 | murmur3 hashing + Pebble key encode/decode |
| Protocol | 9 | wire frame I/O (msgpack + CRC32C) |
| RPC | 13 | codec, dispatch, end-to-end round-trip |
| Transport | 5 | yamux stream round-trip + TLS overhead + stream setup |
| Worker | 4 | registry lookup + factory invocation |

## How to read these numbers

Three buckets:
- **"Just how fast it is"** — protocol marshal, RPC codec, keygroup hashing,
  registry lookup. You can't make these dramatically faster without a rewrite.
- **"Real bug, fix it"** — `pebble.Sync` on every Set, `ReadRPCFrame` not
  pooling, `BarrierAligner_BufferDrain` allocating half a MB,
  `MemoryStore_PrefixScan` allocating per-key. One-line wins live here.
- **"Architectural cost"** — `OperatorChain_*` per-record allocs, fsync floor
  on PebbleDB, JSON decode for `CheckpointMetadata`, TLS handshake floor.
  Need design changes to move the needle meaningfully.

## Notes for re-running

- Full suite walltime is ~18 minutes at `-benchtime=3s -count=3` — coordinator
  alone takes ~7 min because PebbleDB Set/Delete/WriteBatch are fsync-bound
  at ~6.5 ms/op. Engine takes ~3 min because checkpoint marshal at 256 tasks
  is 800 µs/op.
- `make bench` filters Pebble/yamux log noise and writes `bench.out`
  (suitable for `benchstat` directly).
- `make bench-save` runs `-count=5` and archives output to
  `docs/benchmarks/runs/<timestamp>.txt`.

## How to run a single bench

```sh
make bench BENCH=BenchmarkPebbleStore_Set
make bench BENCH_PKGS=./internal/engine/...
make bench BENCH=BenchmarkOperatorChain BENCH_PKGS=./internal/engine/...
make bench BENCH=BenchmarkClientCall_Heartbeat BENCH_PKGS=./internal/rpc/...
```

## How to capture a CPU profile

```sh
make profile-cpu BENCH=BenchmarkPebbleStore_Set
go tool pprof -http=:6060 cpu.prof

make profile-mem BENCH=BenchmarkBarrierAligner_BufferDrain

make trace
go tool trace trace.out
```

## Live profiling against a running coordinator

The coordinator's HTTP server exposes `/debug/pprof/*` on its API port
(default `:4001`).

```sh
make profile-live DURATION=30 PPROF_HOST=http://localhost:4001

# Heap snapshot
go tool pprof -http=:6060 http://localhost:4001/debug/pprof/heap

# Mutex / block contention — start wire with --profile-extra to enable
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

If you don't already have benchstat:
`go install golang.org/x/perf/cmd/benchstat@latest`
