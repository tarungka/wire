# WIP-26 — Codebase complexity audit: catalog of optimization candidates

> **Status:** partially implemented. The audit catalog itself remains
> a catalog. The "Batch 1" subset of low-risk and high-impact
> verified findings has been implemented in the same commit (see the
> *Implemented in this commit* section below). Everything else stays
> a candidate that should graduate to its own focused WIP once
> measured. Findings here are static-analysis results — most are
> *suspected* bottlenecks, not proven ones. Verification (benchmark
> or production trace) is required before fixing.

## Why this WIP exists

The last four WIPs (22–25) were each scoped to a single bottleneck
that surfaced in production: a histogram bucket choice (WIP-22), an
fsync-under-lock submit path (WIP-23), an errgroup race (WIP-24), an
O(N) duplicate-name scan under `c.mu` (WIP-25). They share a pattern
— a single line, lock, or loop dominated p99 once the system was
pushed.

This WIP catalogs the next layer of candidates so we don't have to
rediscover them one production incident at a time. It is deliberately
broad: hot-path allocations, lock-held loops, missing indexes, codec
double-copies, and per-tick scans are all included. Promotion to a
real WIP requires proof — a benchmark, a Grafana panel, or a
synthetic load test that pins the line.

Out of scope: anything WIPs 22, 23, 24, 25 already address.

## Method

Four parallel static scans (one per subsystem) plus spot-checked
verification of the highest-impact findings. Where a scan claimed an
inefficiency that a quick `Read` disproved, the finding was dropped
(e.g., one scan reported a linear scan in `worker.handleCommands`;
inspection showed it is a direct map lookup). Findings below were
either verified by reading the code or are mechanical patterns
(allocation per call, decode per call, lock held across I/O) that
don't need verification beyond the line reference.

---

## Finding catalog

Findings are grouped by subsystem, then ordered roughly by expected
impact. **P** = priority for promotion to a focused WIP.

### Coordinator (control plane)

| # | File:line | Issue | Complexity | P |
|---|---|---|---|---|
| C1 | `internal/coordinator/coordinator.go:409–428` | `allTasksInStatus` does a `store.Get` + `DecodeMsgPack` of `JobAssignmentsKey` on every call. Called twice per `UpdateTaskStatus` (`rpc_handlers.go:173,200`) — once to check all-Running, once to check all-Finished. | Pebble Get + msgpack decode per task status update. With T tasks reporting status, decode of the same assignment map is repeated O(T) times per job lifecycle. | **High** |
| C2 | `internal/coordinator/coordinator.go:314–321` | `jobStateCounts` callback iterates **all** `c.jobs` on every Prometheus scrape under `c.mu.RLock()`. Documented as "uncontended in practice," but cost grows linearly with job count and scrapes are frequent. | O(N) per scrape, one allocation for the counts map. | Medium |
| C3 | `internal/coordinator/coordinator.go:434–462` | `flushHeartbeats` builds the `batch` slice via repeated `append` with no preallocation, while holding `c.mu.Lock()`. Encodes each worker to msgpack inside the lock. | O(W) encodes under exclusive lock; lock held for entire encode loop. | Medium |
| C4 | `internal/coordinator/coordinator.go:328–345` (`EnqueueCommand`) | Acquires `c.mu.Lock()`, looks up `cmdStreams`, *unlocks*, then attempts a non-blocking send on the channel. The unlock-then-send window is unnecessary — the send is bounded — and the lock-release/reacquire dance is paid on every command dispatch. | One extra unlock+lock pair per command on the stream-attached path. | Low |
| C5 | `internal/coordinator/job_manager.go` `ListJobs` (status filter) | Status filter is implemented as a full O(N) scan with predicate. There is no `jobsByStatus` secondary index even though `transitionJob` already knows when status changes. | O(N) per list, even when caller wants only `RUNNING`. | Medium |
| C6 | `internal/coordinator/job_manager.go` `CancelJob` | Loads assignments, then loops tasks issuing one `EnqueueCommand` per task. Each command takes `c.mu` once. | O(T) lock acquires + O(T) RPC enqueues per cancel. | Low |
| C7 | `internal/coordinator/savepoint_manager.go` `ListSavepoints` (~lines 72–91) | `PrefixScan` decodes each savepoint individually and `append`s without preallocation; no result cap, no pagination. | O(S) decode + unbounded append per call. | Low |
| C8 | `internal/coordinator/recovery.go:170, 202` | `recoverJobCheckpoints` and `recoverJobSavepoints` `append` without preallocation. Recovery only, but with 10k+ jobs this dominates restart time. | O(C+S) appends with reallocs. | Low |
| C9 | `internal/coordinator/reconcile.go:88–130` | Worker reconcile builds two maps (`assignedTasks`, `reportedSet`) and walks them with two separate loops to find orphans and missing tasks. | O(T) twice, two map allocations per reconcile. | Low |
| C10 | `internal/coordinator/job_state_machine.go:34–44` | `ValidateTransition` linear-scans a `[]JobStatus` of valid next states. Trivially a `map[from]map[to]bool`. | O(degree) per transition. | Trivial |
| C11 | `internal/coordinator/store_memory.go:47–81` | Sorted-slice insert/delete with `copy()`-shift. Production uses Pebble, but tests and `MemoryStore`-backed runs scale O(N) per write. | O(N) per write in the test/dev backend. | Low (tests only) |

### Engine (data plane hot path)

| # | File:line | Issue | Complexity | P |
|---|---|---|---|---|
| E1 | `internal/engine/operator_chain.go:181–210` (`processEvent`) | Allocates `events := []Event{event}` *per input event* even on the common single-output Map path. Then per-link `next` slice, even when no fan-out is possible. | One slice allocation per event in the steady-state path. At millions of events/sec this is the dominant GC source. | **High** |
| E2 | `internal/engine/watermark_tracker.go:51–81` (`MinWatermark`) | Linear scan over all `numInputs` to find the min watermark. Called once per propagator tick (`watermark_propagator.go:36`); ticks fire even when no input has advanced. | O(N) per tick + tick fires regardless of dirty state. A min-heap keyed by input index would be O(log N) per advance, O(1) peek. | **High** |
| E3 | `internal/engine/state_backend_hashmap.go` (`Put`/`Get`) | Sorted-slice state backend: every `Put` does binary-search-then-shift insert under exclusive `Mutex`. Every `Get` returns a defensive `cloneBytes`. Every iterator call clones the entire matched range up front (lines ~127–143) — "lazy" iteration is not actually lazy. | Per write: O(log N) search + O(N) shift under lock. Per read: full key+value copy. Iterator: O(matching keys) up front. | **High** |
| E4 | `internal/engine/barrier.go` (`IsAligning`/`BufferEvent`) | Per-event `Mutex` lock on `BufferEvent`; even checking `IsAligning()` takes the lock. The hot path for non-aligning inputs (the common case) should not need a mutex. | One lock op per data event in the aligning input. | Medium |
| E5 | `internal/engine/input_reader.go:72–80` | When the barrier aligner's side buffer fills, the reader spin-retries `BufferEvent` with no backoff — burns CPU under back-pressure and blocks one input's frame loop. | Busy-wait, O(1) per retry but unbounded retries during stall. | Medium |
| E6 | `internal/engine/output_writer.go:36–49` | `Event.ToProto()` allocates a fresh `DataRecordMsg` per outbound event; downstream protobuf marshal allocates more buffers. No `sync.Pool` reuse for the message struct or marshal buffer. | Two+ allocations per event sent over the wire. | Medium |
| E7 | `internal/engine/error_handler.go:184–195` | DLQ event construction (`DLQEvent` struct + `err.Error()` string copy + `time.Now().UnixMilli()`) allocates per error. Fine for steady state; pathological under error storms. | One allocation + one syscall per error. | Low |
| E8 | `internal/engine/checkpoint_coordinator.go` (`PendingCommits`) | Append-without-preallocation when collecting transactional sinks awaiting commit. Per-checkpoint, not per-event, but happens at every checkpoint. | O(sinks) appends with reallocs. | Low |

### RPC / Transport / Protocol

| # | File:line | Issue | Complexity | P |
|---|---|---|---|---|
| R1 | `internal/protocol/frame.go:66–98` (`ReadFrame`) | Reads body into a *pooled* buffer (lines 67–73), then copies the payload out into a fresh `make([]byte, ...)` (lines 87–88) so the pooled buffer can be returned. The whole point of the pool is defeated for the largest part of the frame. | One full-payload copy per frame on the read path. | **High** |
| R2 | `internal/rpc/codec.go` `ReadRPCFrame` / `WriteRPCFrame` | Read path always `make([]byte, payloadLen)`; write path goes through `framePool.Get/Put` for the buffer but every write pays the pool round-trip. Streaming RPCs (heartbeats, `WatchCommands`) hit this in their per-frame inner loop. | One allocation per RPC payload read; pool turn per write. | Medium |
| R3 | `internal/transport/stream.go:132–217` (`ReadMessage`) | Up to ~6 distinct `fs.mu` lock acquisitions in a single `ReadMessage` call (fast-path check, CRC error path, decode error path, EOP detection, etc). High-frequency streams pay 6× lock ops per frame. | Mutex acquisitions per frame; cache-line ping-pong on `fs.mu`. | Medium |
| R4 | `internal/transport/mux.go:200–207` (`sessionAcceptLoop`) | Sends accepted streams to a 64-buffered channel with no timeout. A slow `Accept` consumer blocks the entire session — head-of-line blocking for new streams on that connection. | Bounded queue with no fairness/timeout. | Medium |
| R5 | `internal/rpc/client.go:129` (`CallStream`) | Response channel buffered at 16 frames. At ~10k frames/sec that is ~1.6 ms of slack — too small for jittery consumers. Backpressure into the stream reader is essentially immediate. | Hard-coded buffer constant. | Low |
| R6 | `internal/rpc/client.go:132–160` (`CallStream` reader goroutine) | Goroutine reads in a loop; if the caller cancels context but doesn't run `cleanup`, the goroutine blocks indefinitely on `ReadRPCFrame`. No deadline propagation from `ctx`. | Goroutine leak on cancellation if cleanup isn't called. | Medium |
| R7 | `internal/rpc/heartbeat.go` (`HeartbeatSender.Run`) | Worker sends heartbeats at fixed `HeartbeatInterval` regardless of coordinator availability. No backoff when the coordinator is unreachable, unlike `CallWithRetry`. | Constant pressure on a recovering coordinator. | Medium |
| R8 | `internal/protocol/frame.go:36–40, 92–96` | CRC32C is computed on every frame even though Yamux already provides reliable framing on top of TCP. The check has value for catching codec bugs but is double-payment for in-cluster traffic. | Two CRC computations per frame (encode + verify). | Low |
| R9 | `internal/transport/stream.go:196–198` | `lastWatermarks` map is lazily allocated on first `WatermarkMsg`, inside a function that already holds the stream mutex. Should be initialized in `NewFrameStream`. | One-time allocation + lock-held alloc. | Trivial |

### SDK / worker / keygroup

| # | File:line | Issue | Complexity | P |
|---|---|---|---|---|
| S1 | `internal/keygroup/hasher.go:8` | `murmur3.Sum32(key) % uint32(numKeyGroups)` introduces classical modulo bias when `numKeyGroups` is not a power of two. The skew is small (≤1 bit), but it does mean key-group hotspots are not perfectly uniform. | Distribution skew, not a runtime cost. | Low |
| S2 | `sdk/partition_router.go` (`HashRouter`/`RebalanceRouter`/`ForwardRouter`) | Routing goes through an `routeFn` function pointer per event; not specialized per shuffle type. Indirect call cost dominates the hash itself for short keys. | One indirect call per event. | Low |
| S3 | `sdk/partition_router.go` `RebalanceRouter` | Single `atomic.Uint64.Add` shared across all senders for global round-robin. With many parallel instances this is a serialization point. | Atomic contention proportional to instances. | Low |
| S4 | `sdk/aggregator.go` (Sum/Min/Max/Avg) `CreateAccumulator` | Each accumulator allocates a fresh small `[]byte`. Per-window, not per-event, but high-cardinality keyed jobs create many accumulators. | One small alloc per window-key combination. | Medium |
| S5 | `sdk/aggregator.go` `AvgAggregator.GetResult` | Allocates `make([]byte, 8)` for every result emission. | One alloc per fired window. | Low |
| S6 | `sdk/window.go`, `sdk/windowed_stream.go` | `WindowAssigner` is a single interface for tumbling/sliding/session windows. Hot path makes interface calls for fields most window types don't use. Specialized concrete types would let the compiler inline. | Interface dispatch per windowed event. | Low |
| S7 | `sdk/embedded.go` `splitStages` / `findShuffleBetween` (~lines 427–475) | Stage boundaries and shuffle endpoints are recomputed by walking edges + rebuilding sets each time. Once at startup, but still wasteful in the embedded path. | O(E × P) at job start. | Trivial |
| S8 | `sdk/data_stream.go` `autoName` | Linear-probes `g.names` with an incrementing suffix until no collision. Pathological if many operators share a prefix. | O(collisions) per `Name()` call. | Trivial |
| S9 | `sdk/stream_graph.go` `hasCycle` / `topoSort` | Both rebuild the adjacency list (`map[int][]int`) from `edges` on each call. Adjacency could be maintained incrementally on `addEdge`. | O(E) per call, multiple calls per submit. | Trivial |
| S10 | `internal/keygroup/assignment.go` `RescaleMapping` | Old × new range intersection is implemented as a nested loop. Ranges are sorted, so a merge-style pass is O(P_old + P_new). | O(P_old × P_new) per rescale. | Low |
| S11 | `internal/worker/worker.go:243–258` (`buildHeartbeatRequest`) | Takes `RLock` to read `len(w.tasks)` per heartbeat. An `atomic.Int32` task count would avoid the lock entirely. | Uncontended RLock per heartbeat. | Trivial |

(Note: an earlier scan flagged `worker.go` `handleCommands` (line ~365) as an O(N) scan over tasks. Verification: it is a direct `w.tasks[cmd.TaskID]` lookup. Dropped.)

---

## Prioritization

The findings most likely to repeat the WIP-23 / WIP-25 pattern — that
is, dominate p99 once the system is pushed — are:

1. **C1** — `allTasksInStatus` decode-per-call on `UpdateTaskStatus`.
   Worker-side RPC volume scales with task count, so this multiplies
   under any large job.
2. **E1** — Per-event slice allocation in `processEvent`. The data
   plane is the throughput-critical path; everything else is
   amortized by checkpoint or scrape interval.
3. **E2** — `MinWatermark` O(N) scan per tick. Watermarks are the
   metronome of the engine; if they get expensive, everything else
   degrades.
4. **E3** — Sorted-slice state backend with full-key clones.
   Foundational data structure decision; impacts every stateful
   operator.
5. **R1** — `ReadFrame` payload double-copy. Wire-rate cost on every
   inbound frame; pool exists but is half-used.
6. **C2 / C5** — coordinator state-counter scrape and `ListJobs`
   filter. Lower per-call cost but called frequently and grow with
   job count.

Everything else is a fair-game cleanup but should not be pre-empted
ahead of measurement.

## Process for promoting a finding

A finding becomes a real WIP only after:

1. **Reproduction.** Either an existing benchmark
   (`internal/engine/bench_test.go`, `internal/keygroup/bench_test.go`,
   `internal/protocol/bench_test.go`) or a new one shows the
   suspected line is hot.
2. **Quantification.** A Grafana panel against
   `examples/observability-stack` or a `k6` run pins the metric
   (latency tail, CPU%, allocs/op) the finding predicts.
3. **Scope.** The fix is contained — no API changes, no
   cross-subsystem refactor. If it isn't, split it.

If a candidate fails (1) or (2), it stays in this catalog as a known
suspicion. WIP-22..25 each had a Grafana panel or `pprof` trace
attached before the fix; we should hold the same bar here.

## Implemented in this commit

The following findings were implemented as one batch alongside the
audit. Each is a small, contained change; none requires an API or
protocol break. Tests + race detector pass on all affected packages
(the only red test is `TestCheckpointCoordinator_TimeoutAbort`, a
pre-existing flake on `master`).

- **C1** — Added an in-memory `assignments` cache on `Coordinator`,
  populated by `scheduler.scheduleJob` on persist and by recovery.
  `allTasksInStatus` now reads from the cache; the Pebble Get +
  `DecodeMsgPack` only runs on cache miss. `CancelJob` reads the
  same cache.
- **C2** — Added a maintained `jobStatusCounts` map. SubmitJob,
  `transitionJob`, `scheduler.scheduleJob`, and the recovery
  finaliser keep it in sync. `jobStateCounts` (the gauge callback) is
  now O(distinct statuses) instead of O(N).
- **C3** — `flushHeartbeats` preallocates `batch` to `len(c.workers)`.
- **C5** — Added `jobsByStatus map[JobStatus]map[string]*JobMeta`
  and helpers `indexJobByStatus` / `unindexJobByStatus`. Maintained
  alongside `jobStatusCounts`. `ListJobs(statusFilter)` is now
  O(matched).
- **C7** — `ListSavepoints` preallocates the result slice.
- **C10** — `validTransitions` is now `map[from]map[to]struct{}`;
  `ValidateTransition` does a single map lookup instead of a slice
  scan.
- **E1** — `processEvent` has a single-event fast path: while no
  FlatMap fan-out has happened, the current event is tracked in a
  local variable instead of a freshly allocated `[]Event{event}`.
  On the first FlatMap with >1 emission we promote to the slice
  path. Sink consumes the event (matches the previous slice
  behaviour, where Sink simply did not append).
- **R1** — `ReadFrame` no longer pools the body buffer or copies the
  payload out. One allocation per frame, no copy. The pool was
  defeated by the copy-out, so removing both is a net win.
- **R9** — `lastWatermarks` is initialised in `NewFrameStream`
  instead of being lazy-initialised under `fs.mu` on the hot read
  path.
- **S11** — `Worker.activeTasks atomic.Int32` mirrors `len(w.tasks)`.
  `buildHeartbeatRequest` reads the atomic instead of taking
  `w.mu.RLock`. `epoch` is also documented as written-once /
  read-only and accessed without the lock.

What was *not* implemented this round:

- **C4 / C6 / C8 / C9 / C11**: low-impact or test-only.
- **E2** (min-heap watermark), **E3** (state-backend redesign):
  structural changes that need their own WIP and benchmark.
- **E4 / E5 / E6 / E7 / E8**: data-plane changes that should be
  benchmark-driven; not landed here.
- **R2 / R3 / R4 / R5 / R6 / R7 / R8**: each warrants its own focused
  WIP with a network-level reproduction.
- **S1–S10** (excl. S11): SDK-side; needs benchmarks before chasing
  per-event allocations.

## Out of scope

- Algorithmic redesigns (e.g., replacing the hashmap state backend
  with an LSM, switching to a different watermark algorithm). Those
  belong in WIP-04 / WIP-18 follow-ups, not here.
- Anything that requires breaking RPC or wire-format compatibility
  before WIP-01 / WIP-07 are settled.
- "Rewrite the SDK to be zero-alloc" — partition routing, window
  state, and aggregators each need their own bench-driven WIP if we
  decide to chase per-event allocations.

## Index of references

- WIP-22 — RPC duration histogram polluted by `WatchCommands`.
- WIP-23 — Coordinator submit path lock+fsync-bound under load.
- WIP-24 — `TaskSlot.Run` masks operator-chain errors via errgroup
  race.
- WIP-25 — `SubmitJob` duplicate-name check is O(N) under `c.mu`.
