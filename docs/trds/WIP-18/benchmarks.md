# State backend benchmark baseline

Measured on 2026-09-24 with Go 1.25.0, darwin/arm64, Apple M4,
using the B-tree implementation introduced in `42dbc2f`. These are local
observations, not CI thresholds, percentiles, or production capacity estimates.

## Reproduce

```sh
go test ./internal/engine -run '^$' -bench '^BenchmarkStateBackendOperations$' -benchtime=200ms -count=3
go test ./internal/engine -run '^$' -bench '^BenchmarkStateBackendCheckpoint$' -benchtime=3x -count=1
```

The fixture contains eight-byte ordered keys and deterministic pseudorandom
1016-byte values: exactly 1024 logical bytes per entry. Preparation uses atomic
batches of 1024 entries outside timing. Each operation benchmark starts with
1 MiB of state. Put updates existing keys; Get cycles through existing keys;
Iterator constructs, fully traverses and closes an all-key iterator. This is a
warm, single-threaded workload, not insert throughput or concurrent processing.

Checkpoint sizes are 1, 64 and 256 MiB of logical key/value payload. Each sample
contains three successive checkpoints without intervening writes. Timings
include the actual backend Checkpoint call. HashMap serializes a full blob;
Pebble creates a native checkpoint with a flushed WAL and hashes its files.
This includes neither replica upload nor coordinator completion. Native
checkpoint cleanup is outside timing. Initial WAL/compaction state, filesystem
cache and later reuse affect these numbers. Memory figures are Go allocations,
not peak RSS; native snapshot files and retained database state are not B/op.

## Local observations

| Operation, 1 MiB fixture | HashMap observed range | Pebble observed range |
| --- | --- | --- |
| Put existing key | 275–283 ns/op | 3.75–3.83 ms/op |
| Get | 191–223 ns/op | 362–364 ns/op |
| Full iterator | 128–136 µs/op | 160–204 µs/op |

Pebble Put includes `pebble.Sync`; HashMap only modifies volatile memory.
The Put comparison therefore reflects different durability guarantees, not
just the relative efficiency of their indexes.

| Checkpoint payload | HashMap mean | Pebble mean |
| --- | --- | --- |
| 1 MiB | 0.440 ms | 41.9 ms |
| 64 MiB | 20.2 ms | 139 ms |
| 256 MiB | 72.6 ms | 294 ms |

The proposal's expected ~1 ms Pebble checkpoint was not observed in this
fixture. Native checkpoint creation includes WAL handling and full file hashing
in this implementation; it is not just an SST hard-link operation. Neither
these numbers nor the proposal's estimates should be quoted as a general SLA.

## Raw output

```text
BenchmarkStateBackendOperations/hashmap/PutExisting-10         	  854230	       281.1 ns/op	3642.38 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/PutExisting-10         	  907185	       282.8 ns/op	3620.83 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/PutExisting-10         	  858036	       274.8 ns/op	3726.59 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/Get-10                 	 1218861	       191.2 ns/op	5354.45 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/Get-10                 	 1000000	       202.4 ns/op	5060.37 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/Get-10                 	 1000000	       222.5 ns/op	4602.01 MB/s	    1024 B/op	       1 allocs/op
BenchmarkStateBackendOperations/hashmap/Iterator-10            	    1844	    127713 ns/op	8210.39 MB/s	 1179005 B/op	    2060 allocs/op
BenchmarkStateBackendOperations/hashmap/Iterator-10            	    1863	    128617 ns/op	8152.70 MB/s	 1179003 B/op	    2060 allocs/op
BenchmarkStateBackendOperations/hashmap/Iterator-10            	    1848	    135844 ns/op	7718.97 MB/s	 1179003 B/op	    2060 allocs/op
BenchmarkStateBackendOperations/pebble/PutExisting-10          	      57	   3808208 ns/op	   0.27 MB/s	      35 B/op	       0 allocs/op
BenchmarkStateBackendOperations/pebble/PutExisting-10          	      60	   3832426 ns/op	   0.27 MB/s	      34 B/op	       0 allocs/op
BenchmarkStateBackendOperations/pebble/PutExisting-10          	      64	   3751566 ns/op	   0.27 MB/s	      32 B/op	       0 allocs/op
BenchmarkStateBackendOperations/pebble/Get-10                  	  656380	       363.5 ns/op	2817.43 MB/s	    1025 B/op	       1 allocs/op
BenchmarkStateBackendOperations/pebble/Get-10                  	  591676	       362.7 ns/op	2823.43 MB/s	    1025 B/op	       1 allocs/op
BenchmarkStateBackendOperations/pebble/Get-10                  	  685323	       361.7 ns/op	2830.77 MB/s	    1025 B/op	       1 allocs/op
BenchmarkStateBackendOperations/pebble/Iterator-10             	    1482	    159896 ns/op	6557.87 MB/s	 1058219 B/op	    2053 allocs/op
BenchmarkStateBackendOperations/pebble/Iterator-10             	    1218	    178474 ns/op	5875.24 MB/s	 1058345 B/op	    2053 allocs/op
BenchmarkStateBackendOperations/pebble/Iterator-10             	    1330	    203501 ns/op	5152.67 MB/s	 1058306 B/op	    2053 allocs/op
BenchmarkStateBackendCheckpoint/hashmap/1MiB-10                	       3	    439653 ns/op	2385.01 MB/s	 1114112 B/op	       2 allocs/op
BenchmarkStateBackendCheckpoint/hashmap/64MiB-10               	       3	  20173583 ns/op	3326.57 MB/s	70787072 B/op	       2 allocs/op
BenchmarkStateBackendCheckpoint/hashmap/256MiB-10              	       3	  72573792 ns/op	3698.79 MB/s	283125578 B/op	       4 allocs/op
BenchmarkStateBackendCheckpoint/pebble/1MiB-10                 	       3	  41856875 ns/op	  25.05 MB/s	  577589 B/op	     429 allocs/op
BenchmarkStateBackendCheckpoint/pebble/64MiB-10                	       3	 139413819 ns/op	 481.36 MB/s	 2543464 B/op	    1522 allocs/op
BenchmarkStateBackendCheckpoint/pebble/256MiB-10               	       3	 294243111 ns/op	 912.29 MB/s	 9177952 B/op	    5051 allocs/op
```
