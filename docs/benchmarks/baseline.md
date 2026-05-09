# Wire — Performance Baseline

This file is the reference point for performance regressions. Re-capture
with `make bench-save` whenever a hot-path change ships.

## Environment

| Field | Value |
|---|---|
| Date | 2026-05-09 |
| Commit | `4798cbf` (master before benchmark wiring) |
| Go | `go1.23.1 linux/amd64` |
| OS | Linux 6.8.0-111-generic |
| CPU | AMD Ryzen 7 5700X 8-Core (16 logical) |
| BadgerDB mode | in-memory for `BenchmarkSet/Get/StoreLogs`; on-disk tmpdir for `BenchmarkApply` |
| Logger | `zerolog.Disabled` globally (matches production gating) |
| Run | `go test -run=^$ -bench=. -benchmem -benchtime=2s -count=1` |

## Headline findings

These are the bottlenecks that jumped out the first time the suite was run.
They are the leading candidates for Phase 2/3 follow-up work.

1. **gzip `BestCompression` is the heaviest single op.** Marshaling 100
   compressed statements takes **196 µs and 819 KB allocated** (vs.
   7.5 µs uncompressed with one 5.4 KB allocation). Source:
   `internal/command/marshal.go:213`, `gzip.NewWriterLevel(&buf, gzip.BestCompression)`.
   Switching to `DefaultCompression` or `BestSpeed` would likely reclaim
   most of that. Worth measuring after Phase 2.
2. **Logger calls dominated BadgerDB ops at default level.** With
   zerolog at trace level, `BenchmarkSet/size=64` measured ~17 µs/op;
   silencing the logger drops it to 6.7 µs/op. Production must gate
   logs at info+. The trace-level `db.logger.Trace(...)` call before
   every Set is allocating even when it does not emit.
3. **JSON encoder allocates per cell.** A 100×100 `QueryRows` payload
   takes 1.3 ms and **10,019 allocations** — one per cell plus framing.
   Candidates: pre-size the `[][]interface{}` slab, drop the per-row map
   in the associative path, or pool `[]byte` buffers.
4. **HTTP parameterized parser allocates ~30/stmt.** 100 parameterized
   statements parse in 272 µs with 3,206 allocations. The
   `interface{}` decoding through `dec.Decode(&item)` is the cost.
5. **Raft `StoreLogs` batch=256 allocates 1.2 MB.** Each log entry is
   independently msgpack-encoded into a fresh `bytes.Buffer`. Reusing
   a buffer would help; so would batching the inner BadgerDB
   `db.Update` into one transaction (currently one txn per log).

## Headline numbers

`ns/op` is the wall time per iteration; `MB/s` is throughput where it
applies; `B/op` and `allocs/op` are heap pressure per call.

```
goos: linux
goarch: amd64
cpu: AMD Ryzen 7 5700X 8-Core Processor

# Raft FSM Apply (msgpack encode + on-disk BadgerDB Set, fsync-bound)
BenchmarkApply/payload=256-16        14804 ns/op   17.29 MB/s    3952 B/op    64 allocs/op
BenchmarkApply/payload=4096-16       71400 ns/op   57.37 MB/s   32093 B/op    72 allocs/op
BenchmarkApply/payload=65536-16    1083938 ns/op   60.46 MB/s  723222 B/op    77 allocs/op

# BadgerDB KV (in-memory)
BenchmarkSet/size=64-16               6681 ns/op    9.58 MB/s    1667 B/op    30 allocs/op
BenchmarkSet/size=1024-16             9027 ns/op  113.44 MB/s    9041 B/op    37 allocs/op
BenchmarkSet/size=16384-16           31389 ns/op  521.96 MB/s  182912 B/op    46 allocs/op
BenchmarkGet/size=64-16               1595 ns/op   40.12 MB/s     621 B/op    12 allocs/op
BenchmarkGet/size=1024-16             1942 ns/op  527.31 MB/s    3527 B/op    12 allocs/op
BenchmarkGet/size=16384-16           11919 ns/op 1374.60 MB/s   66904 B/op    19 allocs/op
BenchmarkSetUint64-16                 6486 ns/op    1.23 MB/s    1360 B/op    31 allocs/op
BenchmarkGetUint64-16                 1501 ns/op    5.33 MB/s     445 B/op    11 allocs/op
BenchmarkStoreLogs/batch=1-16         9794 ns/op   27.77 MB/s    4742 B/op    57 allocs/op
BenchmarkStoreLogs/batch=16-16      155455 ns/op   28.00 MB/s   76560 B/op   922 allocs/op
BenchmarkStoreLogs/batch=256-16    2518471 ns/op   27.65 MB/s 1237981 B/op 14767 allocs/op

# Protobuf marshal / unmarshal
BenchmarkProtoMarshal/stmts=1-16       392.3 ns/op  147.86 MB/s     64 B/op    1 allocs/op
BenchmarkProtoMarshal/stmts=10-16     1048   ns/op  502.85 MB/s    576 B/op    1 allocs/op
BenchmarkProtoMarshal/stmts=100-16    7464   ns/op  697.62 MB/s   5376 B/op    1 allocs/op
BenchmarkProtoMarshalCompressed/stmts=10-16    157883 ns/op    0.56 MB/s  814675 B/op  21 allocs/op
BenchmarkProtoMarshalCompressed/stmts=100-16   195652 ns/op    0.63 MB/s  819475 B/op  21 allocs/op
BenchmarkProtoMarshalCompressed/stmts=1000-16  378783 ns/op    0.70 MB/s  872083 B/op  22 allocs/op
BenchmarkProtoUnmarshalCommand-16        224.0 ns/op  495.59 MB/s    192 B/op    2 allocs/op

# JSON QueryRows encoder
BenchmarkJSONMarshalQueryRows/rows=1/cols=1-16        819.3 ns/op   75.68 MB/s    248 B/op     5 allocs/op
BenchmarkJSONMarshalQueryRows/rows=1/cols=10-16      2516   ns/op   90.22 MB/s    640 B/op    11 allocs/op
BenchmarkJSONMarshalQueryRows/rows=100/cols=10-16  126696   ns/op   52.87 MB/s  35307 B/op  1002 allocs/op
BenchmarkJSONMarshalQueryRows/rows=100/cols=100-16 1302952  ns/op   54.26 MB/s 381713 B/op 10019 allocs/op
BenchmarkJSONMarshalQueryRowsAssoc-16               398039  ns/op   29.35 MB/s 209376 B/op  3429 allocs/op

# TCP mux dispatch (per-connection 1-byte demux + map + chan send)
BenchmarkMuxDispatch-16               2962  ns/op                    1514 B/op    17 allocs/op

# HTTP request parser (JSON statement array)
BenchmarkParseRequestSimple/stmts=1-16      1347 ns/op   38.60 MB/s    1168 B/op    14 allocs/op
BenchmarkParseRequestSimple/stmts=10-16     9622 ns/op   53.11 MB/s    3784 B/op    99 allocs/op
BenchmarkParseRequestSimple/stmts=100-16   88927 ns/op   57.36 MB/s   31000 B/op   913 allocs/op
BenchmarkParseRequestParameterized-16     271896 ns/op   29.39 MB/s  113416 B/op  3206 allocs/op
BenchmarkParseRequestLarge-16             182291 ns/op   60.73 MB/s   62744 B/op  1814 allocs/op
```

## Notes for re-running

- The full suite walltime is ~115 seconds at `-benchtime=2s -count=1`.
  Use `-count=5` for benchstat-quality numbers (`make bench-save`).
- The legacy `internal/store/` and `internal/pipeline/` packages do not
  currently compile on master. The `BENCH_PKGS` Makefile variable
  scopes runs to the working set: `internal/new/...`,
  `internal/command/...`, `internal/cluster/...`, `internal/tcp/...`,
  `internal/http/...`, `internal/snapshot/...`, `internal/db/...`.
- BadgerDB benches default to in-memory. Set `BENCH_DISK=1` to run them
  against an on-disk tmpdir; numbers will be dominated by fsync.
- Benchmarks deliberately *not* included in this baseline (deferred
  until the OTel work in Phase 2 sets up the necessary stubs):
  HTTP `Query`/`Execute` end-to-end, cluster RPC client, FSM
  `Snapshot`/`Restore` (currently `ErrNotImplemented`).

## How to capture an updated baseline

```sh
make bench-save                     # archives to docs/benchmarks/runs/<ts>.txt
benchstat docs/benchmarks/runs/<old>.txt docs/benchmarks/runs/<new>.txt
```

## How to capture a live profile

```sh
# CPU profile (interactive flame graph in browser)
make profile-live DURATION=30 PPROF_HOST=http://localhost:8081

# Runtime trace (open with `go tool trace`)
curl -o trace.out 'http://localhost:8081/debug/trace?seconds=10'
go tool trace trace.out

# Heap, mutex, block (mutex+block require --profile-extra at startup)
go tool pprof -http=:6060 http://localhost:8081/debug/pprof/heap
go tool pprof -http=:6060 http://localhost:8081/debug/pprof/mutex
go tool pprof -http=:6060 http://localhost:8081/debug/pprof/block
```
