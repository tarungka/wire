# WIP-02 concurrency benchmark baseline

Runtime code at `29c2920`, with the concurrency benchmark additions in this
follow-up. Apple M4, darwin/arm64, Go 1.25.0, GOMAXPROCS 10. Medians of three
200 ms samples, without the race detector. These are desktop microbenchmarks,
not a throughput guarantee or the final post-integration performance result.

```sh
go test ./internal/engine -run '^$' -bench 'Benchmark(OperatorChain|BarrierAligner|EventChannel|DeserializationPlacement)' -benchmem -benchtime=200ms -count=3
```

| Benchmark | ns/op (median) | B/op (median) | allocs/op |
|---|---:|---:|---:|
| OperatorChain_MapPassthrough | 252.1 | 133 | 3 |
| OperatorChain_FlatMap | 461.3 | 365 | 6 |
| OperatorChain_Sink | 77.69 | 5 | 1 |
| OperatorChain_MapPassthrough_WithErrorHandler | 312.8 | 133 | 3 |
| BarrierAligner_BufferDrain | 31654 | 531948 | 112 |
| DeserializationPlacement/reader | 480 | 1443 | 5 |
| DeserializationPlacement/chain | 455.4 | 1443 | 5 |
| EventChannel | 28.25 | 0 | 0 |

The event-channel benchmark isolates a bounded 1024-entry, two-goroutine handoff
of an immutable 1 KiB payload. Deserialization placement compares the same
MessagePack record and bounded handoff, decoding in either producer or consumer;
it excludes network I/O and substantial operator work. Reader placement was
slightly slower for this minimal-work case and uses the same allocation count.
This baseline does not establish a speedup under real operator CPU load.
The barrier benchmark includes its existing buffer allocation/drain workload;
its operation is a whole alignment cycle, not one event.

Rerun after final runtime integration before treating these as WIP-02 release
evidence. No acceptance threshold or waiver is inferred from these results.
