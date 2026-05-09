# WIP-23 — Coordinator submit path is lock+fsync-bound under load

> **Status:** deferred. Diagnosis confirmed via 10-min Grafana sweep
> against `examples/observability-stack`; fix not yet implemented.

## Symptom

Under sustained submit load (k6 at ~100 RPS), three latency tails go
bad together:

| Metric | Observed (10m max) | Expected |
|---|---|---|
| HTTP `POST /api/v1/jobs` p99 | **4.93 s** | sub-50 ms |
| RPC `Heartbeat` p99 | **9.89 s** | sub-10 ms |
| RPC `UpdateTaskStatus` p99 | **9.78 s** | sub-10 ms |
| Pebble `set` p99 | **49 ms** | ~6-12 ms (one fsync) |
| Pebble `write_batch` p99 | **50 ms** | ~6-12 ms |
| Goroutines peak | **445** (symptom) | <50 idle |

Everything else under load is fine — 0 errors over 11 k requests, GC
max pause 5 ms / STW fraction 0.077 %, heap 21.7 MB, RSS 59.5 MB,
FDs at 0.02 % of rlimit, CPU peak 0.72 cores. The system is **not**
CPU-, memory-, or GC-bound.

## Root cause

The submit path serialises on `c.mu` and pays two fsyncs per request.
Worker-side RPCs queue behind submitters on the same mutex.

1. **Two sequential fsyncs per submit.** `SubmitJob`
   (`internal/coordinator/job_manager.go:23`) does:
   - line 61 — `c.store.Set(JobMetaKey(job.ID), data)` (under
     `c.mu.Lock()`, fsync)
   - line 69 — `c.store.Set(JobConfigKey(job.ID), config)` (after
     unlock, fsync)

   The Pebble store always commits with `pebble.Sync`
   (`internal/coordinator/store_pebble.go:115`, `:121`, `:135`), so
   each write blocks for one disk fsync. Floor is ~6 ms; at 100 RPS
   commits queue and p99 climbs to ~50 ms (≈ 8× depth).

2. **`c.mu` is held across the first fsync.**
   `internal/coordinator/job_manager.go:54-66` takes `c.mu.Lock()`,
   does the duplicate-name scan, calls `c.store.Set(JobMetaKey, ...)`
   — which fsyncs — *then* unlocks. Every other goroutine that wants
   `c.mu` (read or write) waits on disk.

3. **Worker RPCs touch the same lock.** Under heavy submit load:
   - `HandleHeartbeat`
     (`internal/coordinator/rpc_handlers.go:44`) takes `c.mu.RLock`
     then `c.mu.Lock`. With Go's RWMutex, an `RLock` queued behind a
     pending `Lock` (the submitter) blocks even if the lock is
     currently held only by other readers — by design, to prevent
     writer starvation.
   - `HandleUpdateTaskStatus`
     (`internal/coordinator/rpc_handlers.go:148`) takes `c.mu.Lock`
     directly.

   That's how a worker's Heartbeat ends up with 9.89 s tail latency:
   it's parked behind a queue of submit lock-holders that are each
   blocked on fsync.

4. **HTTP request pile-up.** Goroutines climbing to 445 is the symptom
   — one goroutine per concurrent HTTP submit, all blocked on
   `c.mu`/fsync.

## Fix plan

Three independent improvements, listed in order of expected impact.
Apply them as separate commits / PRs so each can be measured.

### Fix 1 — Single batched fsync per submit (highest impact / lowest risk)

`internal/coordinator/job_manager.go` `SubmitJob`:

- Replace the two `c.store.Set(...)` calls with a single
  `c.store.WriteBatch([]KVPair{{JobMetaKey, data}, {JobConfigKey, config}})`.
- `WriteBatch` already exists in `internal/coordinator/store_pebble.go:124`
  and commits the whole batch with one `pebble.Sync`.
- Halves the per-submit fsync count without any semantic change.

Expected effect: HTTP submit p99 ~ halves, Pebble `set` rate halves
(replaced by `write_batch` rate at the same overall throughput).

### Fix 2 — Drop the fsync from the critical section

`internal/coordinator/job_manager.go` `SubmitJob`:

- Move the `c.store.WriteBatch(...)` call (after Fix 1) **outside**
  the `c.mu.Lock()` block.
- Insert the in-memory map entry (`c.jobs[job.ID] = job`) under the
  lock; persist outside the lock.
- Trade-off: a crash *between* the in-memory insert and the disk
  commit drops the job. Recovery on restart already rebuilds from
  Pebble (`internal/coordinator/recovery_test.go` exercises this), so
  the worst case is "submitter saw 201, job is gone" — same failure
  mode as a network-partitioned ACK and recoverable by client retry.
- If that trade is unacceptable, alternative: keep the persist under
  the lock but use Go's `sync.Map` or a sharded map for `c.jobs` so
  Heartbeat/UpdateTaskStatus reads don't compete with submits at all.

Expected effect: Heartbeat / UpdateTaskStatus tail latency collapses
back to single-digit ms because they no longer queue behind disk I/O.

### Fix 3 — `pebble.NoSync` for non-critical metadata writes

`internal/coordinator/store_pebble.go`:

- Add a `SetAsync` (or `Set` variant) that uses `pebble.NoSync`.
- Use it for *non-load-bearing* writes (e.g. UpdatedAt timestamp
  refreshes, scheduler-internal status transitions where a duplicate
  task descriptor on recovery is harmless).
- Keep `pebble.Sync` for `JobMetaKey` / `JobConfigKey` writes — losing
  a submitted job on crash is bad.
- This is the riskiest of the three; do it last and only with explicit
  tests that exercise the recovery path.

## Verification

After each fix, re-run the same load profile and re-check:

```sh
cd examples/observability-stack
docker compose up -d --build coordinator worker prometheus grafana
docker rm -f wire-k6-graph-init wire-k6 2>/dev/null
RPS=100 PRE_VUS=500 MAX_VUS=2000 DURATION=2m docker compose --profile k6 run --rm k6
```

Target numbers in Grafana (`Wire / coordinator overview`):

- **HTTP submit p99**: < 50 ms after Fix 1, < 30 ms after Fix 2.
- **Heartbeat / UpdateTaskStatus p99**: < 10 ms after Fix 2 (Fix 1
  doesn't help these directly).
- **Pebble write_batch p99**: ~ Pebble fsync floor (6-12 ms) at this
  RPS.
- **Goroutines steady-state**: < 100 (currently peaks at 445).

Tests:

```sh
go test -race ./internal/coordinator/... ./internal/rpc/...
```

`recovery_test.go` is the load-bearing test for Fix 2 — confirm it
still exercises submit → restart → job present.

## Critical files

- `internal/coordinator/job_manager.go` (lines 23-88 `SubmitJob`)
- `internal/coordinator/store_pebble.go` (lines 112-115 `Set`, 124-138
  `WriteBatch`)
- `internal/coordinator/rpc_handlers.go` (lines 44 `HandleHeartbeat`,
  148 `HandleUpdateTaskStatus`)
- `internal/coordinator/recovery_test.go` (recovery contract for
  Fix 2)

## Out of scope

- Pebble tuning (`MemTableSize`, `WALMinSyncInterval`, etc.). Cheaper
  to remove the fsync from the hot path than to tune around it.
- Sharding the coordinator's in-memory state. The fixes above bring
  per-instance throughput to disk-bound; horizontal scaling is a
  separate, larger project.
- Async submit API (return 202 + job ID, persist asynchronously). Out
  of scope; would change client semantics.
