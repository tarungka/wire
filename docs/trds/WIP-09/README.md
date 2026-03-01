# Coordinator High Availability

> **Feature/Project:** `Coordinator High Availability`
>
> **WIP ID:** `WIP-09`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-03-01`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |
| 0.2 | 2026-03-01 | Tarun Ashok | Reworked: removed Raft, adopted Flink-inspired phased HA strategy with PebbleDB persistence |

---

## 1. Overview

### 1.1 Problem Statement

Wire's architecture document describes the Coordinator as "lightweight and generally stateless (relying on an external metadata store or leader election for HA)" but the HA mechanism itself is entirely unspecified. The operations document states that when the Coordinator crashes, "Workers lose heartbeat. Workers self-terminate. External Supervisor (K8s/Systemd) restarts Coordinator. Workers rejoin." This is a single-point-of-failure model: the Coordinator is the sole control plane node, and its loss halts all running jobs until an external process restarts it.

Without Coordinator HA, Wire cannot claim production readiness. A single Coordinator crash during a checkpoint coordination window can leave jobs in an inconsistent state. A crash during task deployment can leave workers with orphaned tasks. There is no mechanism for metadata to survive a Coordinator restart, and no defined behavior for failover scenarios.

**Current state of the Coordinator:** The Coordinator is minimally implemented. `cmd/main.go` parses flags, sets up logging, prints the logo, and blocks on a signal. There is no RPC server, no job submission path, no task scheduling, and no worker orchestration. The `CheckpointCoordinator` in `internal/engine/` handles local barrier alignment and timeout management for a single node, but it has no persistence layer, no replication, and no recovery logic. Building HA for a Coordinator that does not yet exist as a functioning control plane would be premature.

### 1.2 Proposed Solution (Technical Summary)

Implement Coordinator High Availability using a phased approach inspired by Apache Flink's HA architecture. Instead of embedding a full Raft consensus group into a not-yet-built Coordinator, Wire will follow Flink's proven pattern: **persist metadata durably, make failover a recovery problem, and keep the HA backend pluggable.**

The strategy has four phases, sequenced so each builds on the previous:

1. **Phase A (Prerequisite, separate WIP):** Single-node Coordinator with PebbleDB persistence and crash recovery. All Coordinator metadata (job graphs, task assignments, checkpoint records, worker registrations) is persisted to a local PebbleDB instance. On restart, the Coordinator reconstructs its in-memory state from PebbleDB. This is the foundation — you cannot replicate state that does not exist.

2. **Phase B: Leader election and fencing.** A pluggable `LeaderElection` interface allows Wire to support multiple election backends: Kubernetes lease, file-based lock, or a future embedded election protocol. The elected leader holds an epoch (fencing token). All commands from the Coordinator carry the current epoch. Workers and storage systems reject commands with stale epochs, preventing split-brain.

3. **Phase C: Standby Coordinator with recovery-from-storage.** A standby Coordinator watches for leader failure. On failover, the new leader reads the persisted PebbleDB metadata (from shared storage or a replicated copy) and reconstructs in-memory state. This follows Flink's model: the new JobManager reads persisted job metadata from the filesystem and rebuilds its view of the world.

4. **Phase D (Future): Re-evaluate embedded consensus.** Once the Coordinator is fully implemented and deployment requirements are clear, evaluate whether embedded Raft is needed. Wire's zero-dependency constraint may eventually make embedded Raft the right choice, but the clean `MetadataStore` and `LeaderElection` interfaces make this a drop-in replacement, not a rewrite.

**Why PebbleDB, not BoltDB:** Wire already uses Pebble (CockroachDB's pure-Go LSM engine) for worker-side state backends (`docs/state-backend.md`). Using the same engine for Coordinator metadata means one engine everywhere — reducing cognitive load, debug tooling, and dependency surface. Unlike BoltDB's single-writer lock, Pebble handles concurrent writes (heartbeat summaries, checkpoint completions, task status updates) via MemTables + WAL without lock contention. Pebble also supports near-zero-cost hard-link snapshots (milliseconds), simplifying backup and future HA snapshot transfer.

**Why not Raft now:** Raft replicates state machine commands across a consensus group. Wire's Coordinator state machine does not exist yet — there is no FSM to replicate. Additionally, Apache Flink, the system Wire is most closely modeled after, does not use Raft. Flink uses ZooKeeper (or Kubernetes leases) for leader election only, with job metadata persisted to a filesystem (HDFS/S3). Failover is a recovery problem: the new leader reconstructs state from persisted metadata. This is simpler, proven at scale, and a better fit for Wire's current maturity level. Raft is not rejected — it is explicitly deferred as a future option once the Coordinator is mature enough to benefit from it.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define persistent metadata store for Coordinator state (PebbleDB) | Multi-region or geo-distributed HA |
| Specify crash recovery protocol (reconstruct from PebbleDB) | Worker-side HA (workers are stateless executors) |
| Define pluggable HA interfaces (`MetadataStore`, `LeaderElection`) | Automatic Coordinator scaling without downtime |
| Define failover protocol (leader change, worker re-registration) | Data plane replication |
| Specify fencing token / epoch protocol for split-brain prevention | External consensus system integration (etcd, ZooKeeper) |
| Document phased HA roadmap from single-node to multi-node | Embedded Raft consensus (deferred to Phase D) |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Metadata durability | None (in-memory only) | Survives Coordinator restart | Crash recovery test |
| Job survival across restart | 0% (all jobs lost) | 100% of RUNNING jobs resume after restart | Integration test |
| Coordinator restart time | Infinite (manual re-submission) | < 10 seconds (PebbleDB state reconstruction) | Automated test |
| Failover time (Phase B) | Infinite (manual restart) | < 15 seconds (election + recovery) | Automated failover test |
| Zero external dependencies for HA | Violated (requires K8s/Systemd) | Fully embedded (Phase D) | Architecture review |
| Fencing correctness | N/A | Stale-epoch commands rejected 100% | Fencing validation test |

---

## 2. Architecture & System Design

### 2.1 High-Level Architecture

```
Phase A: Single-Node Coordinator with Persistence

  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
  │   Worker 1   │────>│              │<────│   Worker 3   │
  └──────────────┘     │  Coordinator │     └──────────────┘
                       │   (Active)   │
  ┌──────────────┐     │              │
  │   Worker 2   │────>│  ┌────────┐  │
  └──────────────┘     │  │PebbleDB│  │
                       │  │metadata│  │
                       │  └────────┘  │
                       └──────────────┘

Phase B+C: Leader Election + Standby with Recovery

  ┌──────────────┐     ┌──────────────┐          ┌──────────────┐
  │   Worker 1   │────>│ Coordinator  │          │ Coordinator  │
  └──────────────┘     │  (LEADER)    │          │  (STANDBY)   │
                       │  epoch: 42   │          │              │
  ┌──────────────┐     │              │          │              │
  │   Worker 2   │────>│  ┌────────┐  │          │  ┌────────┐  │
  └──────────────┘     │  │PebbleDB│  │──copy──> │  │PebbleDB│  │
                       │  │metadata│  │  or      │  │metadata│  │
  ┌──────────────┐     │  └────────┘  │  shared  │  └────────┘  │
  │   Worker 3   │────>│              │  storage │              │
  └──────────────┘     └──────┬───────┘          └──────────────┘
                              │
                       ┌──────┴───────┐
                       │    Leader    │
                       │   Election   │
                       │  (pluggable) │
                       └──────────────┘

Port 4001: HTTP API (REST, health checks, metrics)
```

### 2.2 Component Breakdown

**Component 1:** Metadata Store (PebbleDB)

* **Responsibility:** Durable persistence of all Coordinator metadata: job graphs, task assignments, checkpoint completion records, worker registrations, and cluster configuration. Source of truth for crash recovery.
* **Technology:** PebbleDB (`github.com/cockroachdb/pebble`), the same LSM engine used for worker-side state backends. Coordinator instance uses tuned-down configuration (smaller MemTable, smaller block cache) since metadata volume is much smaller than worker state.
* **Interactions:** Written by the Coordinator's control loops on every state mutation. Read on startup for state reconstruction. Snapshotted via Pebble's hard-link checkpoint for backup and future HA snapshot transfer.

**Component 2:** Coordinator State Machine

* **Responsibility:** In-memory representation of the Coordinator's control plane state. Drives all decisions: job scheduling, task assignment, checkpoint coordination, failure recovery. Reconstructed from PebbleDB on startup.
* **Technology:** Go structs. Loaded from PebbleDB via prefix scans on startup. Updated in-memory and persisted to PebbleDB atomically (using Pebble's `Batch` for multi-key writes).
* **Interactions:** Queried by the job manager, checkpoint coordinator, and resource manager. Mutations are applied to both the in-memory state and PebbleDB in a single code path.

**Component 3:** Leader Election (Phase B)

* **Responsibility:** Ensures exactly one Coordinator is the active leader at any time. Provides epoch (fencing token) that increments on every leadership change.
* **Technology:** Pluggable via `LeaderElection` interface. Implementations: Kubernetes lease (production), file-based lock (development/single-host), future embedded election.
* **Interactions:** The Coordinator starts in standby mode and calls `LeaderElection.Campaign()`. On winning, it receives a `LeaderContext` with the current epoch and a cancellation signal. On losing leadership (context cancelled), the Coordinator stops processing and re-enters standby.

**Component 4:** Fencing (Phase B)

* **Responsibility:** Prevents split-brain by ensuring stale leaders cannot issue commands that are acted upon. Every command from the Coordinator carries the current epoch. Workers and storage systems reject commands with epochs older than the last seen epoch.
* **Technology:** Monotonically increasing `uint64` epoch, persisted in PebbleDB under `cluster/epoch`. Workers track the highest epoch seen and reject lower epochs.
* **Interactions:** Embedded in all Coordinator-to-worker RPCs and checkpoint coordination messages.

### 2.3 Data Flow

**Normal Operation (Single-Node, Phase A):**

1. Worker sends heartbeat to Coordinator (HTTP or RPC on port 4001).
2. Coordinator updates worker last-seen timestamp in-memory and writes to PebbleDB (`workers/{worker_id}/meta`).
3. Coordinator triggers checkpoint: writes `CheckpointTriggered` to PebbleDB, sends barriers to workers.
4. Workers complete checkpoint and ACK to Coordinator.
5. Coordinator writes `CheckpointCompleted` record to PebbleDB with offsets and state paths.
6. PebbleDB WAL ensures all writes survive a crash.

**Crash Recovery (Single-Node, Phase A):**

1. Coordinator process crashes or is killed.
2. External supervisor (K8s/Systemd) restarts Coordinator.
3. Coordinator opens PebbleDB directory. WAL replay recovers any writes not yet flushed to SSTables.
4. Coordinator performs prefix scans to reconstruct in-memory state:
   - `jobs/` prefix → all job metadata and checkpoint records
   - `workers/` prefix → worker registrations (stale, but provides baseline)
   - `cluster/` prefix → cluster configuration and epoch
5. Coordinator marks all worker registrations as stale and waits for workers to re-register.
6. Workers detect heartbeat timeout, reconnect, and re-register with their current running tasks.
7. Coordinator reconciles: matches worker-reported tasks against persisted task assignments.
8. Jobs resume from the last completed checkpoint recorded in PebbleDB.

**Failover (Multi-Node, Phase B+C):**

1. Active leader crashes.
2. Leader election backend detects failure (lease expiry, lock release).
3. Standby Coordinator wins election, receives new epoch (e.g., epoch 43).
4. New leader opens its PebbleDB (populated via shared storage or prior sync).
5. Reconstructs in-memory state from PebbleDB (same as crash recovery).
6. Begins accepting worker heartbeats, rejecting any with epoch < 43.
7. Workers detect leader loss, discover new leader, re-register.
8. New leader reconciles task assignments and aborts any in-flight checkpoint not marked complete.

---

## 3. API Design

### 3.1 HA Interfaces

Inspired by Flink's `HighAvailabilityServices`, Wire defines two core interfaces that keep the HA backend swappable.

#### 3.1.1 MetadataStore Interface

```go
// MetadataStore provides durable persistence for Coordinator metadata.
// The default implementation uses PebbleDB. Future implementations could
// use shared filesystems, object stores, or replicated storage.
type MetadataStore interface {
    // Get retrieves a value by key. Returns nil, nil if key does not exist.
    Get(key []byte) ([]byte, error)

    // Set persists a key-value pair durably.
    Set(key, value []byte) error

    // Delete removes a key.
    Delete(key []byte) error

    // WriteBatch atomically applies a batch of writes.
    WriteBatch(batch []KVPair) error

    // PrefixScan iterates over all keys with the given prefix.
    // The callback receives each key-value pair. Return false to stop iteration.
    PrefixScan(prefix []byte, fn func(key, value []byte) bool) error

    // Snapshot creates a point-in-time snapshot of the store.
    // Returns the path to the snapshot directory.
    Snapshot(destDir string) error

    // Close releases all resources.
    Close() error
}

// KVPair represents a key-value pair for batch writes.
type KVPair struct {
    Key    []byte
    Value  []byte
    Delete bool // If true, this is a delete operation.
}
```

#### 3.1.2 LeaderElection Interface

```go
// LeaderElection provides pluggable leader election for Coordinator HA.
// Implementations: Kubernetes lease, file lock, future embedded election.
type LeaderElection interface {
    // Campaign blocks until this node becomes the leader or the context is cancelled.
    // On success, returns a LeaderContext. The caller must stop acting as leader
    // when LeaderContext.Done() is closed (leadership lost).
    Campaign(ctx context.Context, nodeID string) (*LeaderContext, error)

    // Resign voluntarily gives up leadership. Used for graceful shutdown.
    Resign(ctx context.Context) error

    // GetLeader returns the current leader's node ID and address.
    // Returns ("", "", ErrNoLeader) if no leader is elected.
    GetLeader(ctx context.Context) (nodeID string, addr string, err error)

    // Close releases all resources.
    Close() error
}

// LeaderContext is returned when a node wins an election.
type LeaderContext struct {
    // Epoch is the fencing token for this leadership term.
    // Monotonically increasing. All commands carry this epoch.
    Epoch uint64

    // Ctx is cancelled when leadership is lost.
    Ctx context.Context

    // Cancel allows the leader to voluntarily stop (calls Resign internally).
    Cancel context.CancelFunc
}
```

#### 3.1.3 Fencing Token / Epoch Protocol

Every RPC and control message from the Coordinator to workers includes the current epoch:

```go
type CoordinatorCommand struct {
    Epoch   uint64 // Fencing token — current leadership term.
    Type    CommandType
    Payload []byte
}
```

**Worker-side fencing logic:**

1. Worker tracks `highestSeenEpoch uint64` (persisted to local disk on update).
2. On receiving a command:
   - If `command.Epoch < highestSeenEpoch`: reject the command, respond with `ErrStaleEpoch`.
   - If `command.Epoch >= highestSeenEpoch`: update `highestSeenEpoch`, process the command.
3. On re-registration with a new leader, the worker sends its `highestSeenEpoch`. The new leader must have an epoch >= the worker's highest seen epoch.

This prevents a zombie leader (one that has lost its lease but hasn't realized it) from issuing commands that are acted upon. The epoch provides the same safety guarantee as Raft's term number, without requiring full consensus.

### 3.2 Coordinator Failover Protocol

#### 3.2.1 Leader Discovery

Workers and external clients discover the current leader through one of two mechanisms:

**Mechanism A: HTTP Redirect**

Any Coordinator node (leader or standby) exposes the HTTP API on port 4001. If a standby receives a write request, it responds with:

```
HTTP/1.1 307 Temporary Redirect
Location: http://<leader-http-addr>/api/v1/jobs
X-Wire-Leader-Id: node-1
X-Wire-Leader-Addr: node1:4001
X-Wire-Leader-Epoch: 42
```

**Mechanism B: Leader Address Query**

```
GET /api/v1/cluster/leader
```

**Response (200 OK) — served by any node:**

```json
{
  "leader_id": "node-1",
  "leader_http_addr": "node1:4001",
  "leader_epoch": 42,
  "is_self": false
}
```

Workers cache the leader address and use it for all subsequent RPCs. On connection failure, workers re-query any known Coordinator address.

#### 3.2.2 Worker Re-registration After Failover

When a worker detects that its heartbeat or RPC calls to the leader are failing:

1. Worker enters a **leader-discovery loop**: iterates through its list of known Coordinator addresses, calling `GET /api/v1/cluster/leader` on each.
2. Once a new leader is found, the worker sends a `RegisterWorker` RPC containing:
   - `worker_id`: Stable identifier for this worker.
   - `task_slots_total`: Number of task slots available.
   - `running_tasks`: List of task IDs currently executing on this worker.
   - `highest_seen_epoch`: The highest epoch the worker has seen (for fencing validation).
3. The new leader compares the worker's reported running tasks against the persisted task assignment records in PebbleDB. Three outcomes:
   - **Match:** Task is assigned to this worker in PebbleDB. No action needed.
   - **Orphaned task:** Worker reports a task not in PebbleDB. Leader instructs worker to cancel it.
   - **Missing task:** PebbleDB shows a task assigned to this worker, but worker does not report it. Leader marks the task as FAILED and initiates recovery.

#### 3.2.3 Job Survival Across Failover

Jobs survive Coordinator failover because all job metadata is persisted in PebbleDB:

- **RUNNING jobs:** The new leader finds the job in RUNNING state in PebbleDB. It waits for workers to re-register and reconcile task assignments. If all tasks are accounted for, the job continues without interruption.
- **DEPLOYING jobs:** The new leader finds the job in DEPLOYING state. It re-issues task deployment commands to workers. If workers already received and started the tasks, the reconciliation in 3.2.2 handles the overlap.
- **FAILING jobs:** The new leader picks up the recovery workflow. It selects the latest completed checkpoint from PebbleDB and re-deploys tasks.
- **In-flight checkpoints:** Any checkpoint that was triggered but not completed at the time of failover is aborted. The new leader triggers a fresh checkpoint to establish a clean baseline.

---

## 4. Data Model & Storage

### 4.1 PebbleDB Keyspace

All Coordinator metadata is stored in a single PebbleDB instance with prefix-based namespaces. Keys are lexicographically ordered byte strings. Prefix scans replace BoltDB-style bucket iteration.

**Namespace: `jobs/`**

| Key | Value Type | Description |
| -- | -- | -- |
| `jobs/{job_id}/meta` | JobMeta (msgpack) | Core job metadata (name, status, parallelism, config hash) |
| `jobs/{job_id}/graph` | []byte | Serialized optimized JobGraph (operator chain, shuffle edges) |
| `jobs/{job_id}/assignments` | TaskAssignmentMap (msgpack) | Mapping of task_id to worker_id for all parallel task instances |
| `jobs/{job_id}/checkpoints/latest` | int64 (big-endian) | ID of the latest completed checkpoint |
| `jobs/{job_id}/checkpoints/{cp_id}` | CheckpointMeta (msgpack) | Metadata for a specific checkpoint (offsets, state paths, timestamp) |
| `jobs/{job_id}/savepoints/{sp_id}` | SavepointMeta (msgpack) | Savepoint metadata (path, status, trigger time) |

**Namespace: `workers/`**

| Key | Value Type | Description |
| -- | -- | -- |
| `workers/{worker_id}/meta` | WorkerMeta (msgpack) | Worker registration (address, task slots total/available, last heartbeat) |
| `workers/{worker_id}/tasks` | []string (msgpack) | List of task IDs currently assigned to this worker |

**Namespace: `cluster/`**

| Key | Value Type | Description |
| -- | -- | -- |
| `cluster/config` | ClusterConfig (msgpack) | Global cluster configuration (checkpoint interval, default parallelism) |
| `cluster/epoch` | uint64 (big-endian) | Current leadership epoch (fencing token) |
| `cluster/leader` | LeaderInfo (msgpack) | Current leader node ID and address |

Serialization uses `hashicorp/go-msgpack` (already in `go.mod`) for structured values. Fixed-size integers (epoch, checkpoint ID) are stored as big-endian bytes for correct lexicographic ordering.

### 4.2 State Mutation Protocol

All mutations to Coordinator state follow a write-through protocol:

1. Compute the new state in memory.
2. Write to PebbleDB (single key via `Set`, or multi-key via `WriteBatch` for atomic operations).
3. PebbleDB WAL fsync ensures durability before the write returns.
4. Update in-memory state.
5. If PebbleDB write fails, do not update in-memory state. Return error to caller.

For operations that require atomicity across multiple keys (e.g., job submission creates `jobs/{id}/meta`, `jobs/{id}/graph`, and `jobs/{id}/assignments` simultaneously), use Pebble's `Batch` with a single `Commit()` call.

### 4.3 Storage Considerations

* **Data volume:** Metadata store size is proportional to the number of active jobs, tasks, and retained checkpoints. For a cluster with 100 jobs, 1000 tasks, and 10 checkpoints per job, the store is approximately 10–50 MB.
* **Write patterns:** Coordinator writes are bursty (checkpoint completions, job submissions) with a steady background of heartbeat updates. Pebble's LSM architecture handles this well — writes go to the MemTable and WAL, with background compaction merging SSTables.
* **Compaction:** Pebble's automatic compaction keeps the store size bounded. Deleted keys (old checkpoints, deregistered workers) are cleaned up during compaction cycles.
* **Tuning for Coordinator:** The Coordinator's PebbleDB instance should use smaller settings than worker state backends:
  - MemTable size: 4 MB (vs 64 MB for workers)
  - L0 compaction threshold: 4 files
  - Block cache: 8 MB (vs 256 MB for workers)
  - WAL: enabled, fsync on commit (durability guarantee)
* **Backup:** Pebble's `Checkpoint()` creates a directory of hard links to the current SSTables. This completes in milliseconds regardless of store size and produces a consistent snapshot for backup or HA replication.
* **Disk requirements:** Recommended minimum: 512 MB for the metadata directory. Actual usage will be much smaller (tens of MB) but headroom accounts for WAL growth during bursty writes.

---

## 5. Design Decisions & Trade-offs

### Decision 1: Pluggable HA with local persistence as default (not embedded Raft)

|  |  |
| -- | -- |
| **Context** | Wire's vision mandates "zero external dependencies." The Coordinator needs HA with durable metadata. However, the Coordinator is minimally implemented — there is no state machine to replicate. Apache Flink, the closest comparable system, uses leader election + persistent storage for HA, not embedded consensus. |
| **Options Considered** | (A) Embedded HashiCorp Raft, (B) Flink-inspired: persistent metadata + pluggable leader election, (C) External etcd cluster, (D) Leaderless with CRDTs |
| **Decision** | Option B: Flink-inspired phased approach with pluggable interfaces |
| **Rationale** | You cannot replicate state that does not exist. The Coordinator must first persist its metadata durably and implement crash recovery before any replication is meaningful. Flink has proven that leader election + persistent storage provides production HA without embedded consensus. Clean interfaces (`MetadataStore`, `LeaderElection`) allow Raft to be introduced later as a drop-in implementation if deployment requirements demand it. |
| **Trade-offs Accepted** | Phase A (single-node persistence) does not provide multi-node HA — an external supervisor is still needed to restart the Coordinator. Full HA requires reaching Phase B+C. However, this phased approach is implementable incrementally, whereas Raft requires building everything at once. |
| **Revisit Trigger** | When the Coordinator is fully implemented (job submission, task scheduling, worker orchestration) and deployment requirements demand sub-second failover without external supervisors. At that point, evaluate embedded Raft as a `LeaderElection` + `MetadataStore` implementation. |

### Decision 2: PebbleDB for Coordinator metadata (not BoltDB)

|  |  |
| -- | -- |
| **Context** | Coordinator metadata must survive process restarts. An embedded key-value store is needed. |
| **Options Considered** | (A) BoltDB (`go.etcd.io/bbolt`), (B) BadgerDB (`dgraph-io/badger`), (C) PebbleDB (`github.com/cockroachdb/pebble`) |
| **Decision** | Option C: PebbleDB |
| **Rationale** | **Consistency:** Wire already uses Pebble for worker-side state backends (`docs/state-backend.md`). One engine everywhere reduces cognitive load, debug tooling, and dependency surface. **Concurrency:** BoltDB has a single-writer lock. The Coordinator receives concurrent heartbeat updates, checkpoint completions, and task status changes. Pebble handles concurrent writes via MemTables + WAL without lock contention. **Snapshots:** Pebble supports near-zero-cost hard-link snapshots, simplifying backup and future HA snapshot transfer. **Industry precedent:** CockroachDB uses Pebble for both data and metadata storage. Unifying storage engines is modern best practice. |
| **Trade-offs Accepted** | PebbleDB has higher baseline memory usage than BoltDB. The Coordinator instance must be tuned down (smaller MemTable, smaller block cache). Pebble is a more complex codebase than BoltDB, though Wire already depends on it conceptually via the state backend design. |
| **Revisit Trigger** | If Coordinator metadata volume is so small that Pebble's LSM overhead is measurably wasteful. In practice, the metadata fits in a single MemTable and this is unlikely to matter. |

### Decision 3: Leader-only writes (no multi-leader)

|  |  |
| -- | -- |
| **Context** | Control plane operations (job submissions, checkpoint coordination) must be consistent. |
| **Options Considered** | (A) Leader-only writes with standby redirect, (B) Multi-leader with conflict resolution, (C) Leaderless with CRDTs |
| **Decision** | Option A: Leader-only writes |
| **Rationale** | A single active Coordinator is the simplest correct approach. Multi-leader adds conflict resolution complexity that is unnecessary for a control plane. The Coordinator's write rate is low (job submissions, checkpoint completions) — a single leader is not a bottleneck. This decision is unchanged regardless of whether HA is implemented via Raft or via leader election + persistent storage. |
| **Trade-offs Accepted** | All writes must pass through the leader. Leader becomes a bottleneck only if write rate is extremely high (unlikely for metadata). |
| **Revisit Trigger** | If control plane write rate exceeds 10,000 ops/sec (extremely unlikely for metadata). |

### Decision 4: In-memory heartbeats with periodic persistence (not per-heartbeat writes)

|  |  |
| -- | -- |
| **Context** | Worker heartbeats update the `last_seen` timestamp. Writing every heartbeat to PebbleDB would generate unnecessary I/O. |
| **Options Considered** | (A) Every heartbeat written to PebbleDB, (B) In-memory only, (C) Hybrid: in-memory with periodic PebbleDB flush |
| **Decision** | Option C: Hybrid approach |
| **Rationale** | Workers send heartbeats every 1–5 seconds. Writing each to PebbleDB is wasteful. Instead, the Coordinator maintains an in-memory `last_seen` map and periodically (every 30s) flushes a heartbeat summary to PebbleDB. On crash recovery, the Coordinator has a recent-enough baseline and quickly re-establishes liveness through worker re-registration. |
| **Trade-offs Accepted** | Up to 30 seconds of heartbeat data can be lost on crash. The recovering Coordinator must wait for workers to re-register before it has full liveness information. This is acceptable because workers self-terminate and reconnect after heartbeat timeout anyway. |
| **Revisit Trigger** | If 30 seconds of staleness causes incorrect failure detection after recovery. Reduce the flush interval if needed. |

### Decision 5: Flink-inspired recovery model (persist metadata, reconstruct on restart)

|  |  |
| -- | -- |
| **Context** | After a Coordinator crash or failover, the new/restarted Coordinator must reconstruct its control plane state. |
| **Options Considered** | (A) Raft FSM replay (replicate and replay log), (B) Recovery from persistent storage (Flink model), (C) Full state transfer from standby |
| **Decision** | Option B: Recovery from persistent storage |
| **Rationale** | This is exactly what Flink does. The JobManager persists job metadata to HDFS/S3. On failover, the new JobManager reads the persisted metadata and reconstructs its in-memory state. Wire's equivalent: PebbleDB is the persistent store, and prefix scans reconstruct the in-memory state. This approach is simpler than Raft FSM replay, does not require a consensus group, and is proven at scale. The `MetadataStore` interface abstracts the storage backend, so future implementations could use shared filesystems, object stores, or even a Raft-backed store. |
| **Trade-offs Accepted** | Recovery time depends on the metadata volume and PebbleDB read speed. For typical metadata sizes (10–50 MB), reconstruction takes < 1 second. This is slower than Raft FSM (which is always up-to-date on followers) but fast enough for production use. |
| **Revisit Trigger** | If recovery time exceeds acceptable thresholds due to metadata volume growth. Mitigation: pre-warm standby by periodically syncing PebbleDB snapshots. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | **Coordinator crashes during checkpoint coordination** | On recovery, the Coordinator reads PebbleDB. If `CheckpointTriggered` was written but `CheckpointCompleted` was not, the checkpoint is aborted. Workers that started snapshotting will time out and discard the partial checkpoint. The recovered Coordinator triggers a fresh checkpoint. | One checkpoint lost. Next checkpoint succeeds normally. | Medium |
| 2 | **Coordinator crashes during job submission** | The PebbleDB `WriteBatch` for job creation (meta + graph + assignments) either committed fully or not at all (atomic batch). If committed, the recovered Coordinator finds the job in CREATED state and proceeds. If not committed, the job does not exist. The client must retry (safe because job submission is idempotent by job name). | Client must retry. No inconsistency. | Medium |
| 3 | **Worker re-registration after failover (Phase B+C)** | Workers detect leader loss via heartbeat timeout. They enter a leader-discovery loop, querying known Coordinator addresses. The new leader validates the worker's `highest_seen_epoch` against its own epoch. Workers reporting tasks are reconciled against PebbleDB records. | Brief task processing pause during re-registration (seconds). | Medium |
| 4 | **Stale leader sends command with old epoch (split-brain prevention)** | Worker receives a command with `epoch < highestSeenEpoch`. Worker rejects the command with `ErrStaleEpoch`. The stale leader eventually discovers it is no longer the leader (election backend notification or repeated rejections) and stops processing. | Stale commands are safely rejected. No split-brain. | High |
| 5 | **Coordinator crashes and PebbleDB WAL is corrupted** | PebbleDB's WAL replay detects corruption. The Coordinator fails to start. Operator must restore from the last PebbleDB snapshot (created by the periodic backup). If no snapshot exists, metadata is lost and jobs must be re-submitted. | Data loss proportional to time since last snapshot. | Critical |
| 6 | **Network partition isolates leader from workers (Phase B+C)** | Workers cannot reach the leader. Workers self-terminate after heartbeat timeout. The leader election backend detects the leader is isolated and revokes its lease. A standby on the other side of the partition becomes the new leader. When the partition heals, workers reconnect and re-register with the new leader. | Jobs on partitioned workers fail and recover from checkpoint. | High |
| 7 | **Standby Coordinator has stale PebbleDB snapshot** | The standby becomes leader and reconstructs state from a stale snapshot. Workers re-register and report their current tasks. The new leader reconciles: tasks reported by workers but not in PebbleDB are treated as valid (the snapshot missed them). Tasks in PebbleDB but not reported by any worker are marked FAILED. The reconciliation protocol self-heals the metadata gap. | Brief reconciliation period. Some tasks may be unnecessarily restarted. | Medium |
| 8 | **Leader election backend fails (Phase B)** | If the election backend (e.g., Kubernetes API server) is unavailable, no election can occur. The current leader continues operating if it holds a valid lease. If the lease expires, the Coordinator enters standby and stops processing until the election backend recovers. | Control plane unavailable until election backend recovers. | High |
| 9 | **Worker connects to standby and sends a write request** | The standby does not process the write. It returns an HTTP 307 redirect to the leader's address. The worker retries at the leader. | One additional round trip. | Low |

---

## 7. Security & Compliance

### 7.1 Metadata Data Protection

* **Encryption at rest:** PebbleDB stores metadata in `--coordinator-data-dir`. Encryption at rest depends on the underlying filesystem or volume encryption (e.g., LUKS, dm-crypt, EBS encryption). Wire does not implement application-level encryption for the metadata store.
* **Sensitive metadata:** Job configurations stored in PebbleDB may reference connector credentials via `${ENV_VAR}` substitution. The resolved values are never stored in PebbleDB — only the variable references. Actual credentials are resolved at runtime on the worker.

### 7.2 Fencing Token Security

* **Epoch integrity:** The epoch is persisted in PebbleDB and monotonically increasing. A newly elected leader reads the current epoch, increments it, and persists the new value before issuing any commands. This prevents epoch reuse even after a crash.
* **Epoch validation:** Workers validate the epoch on every command. A compromised or buggy Coordinator cannot issue commands with a fabricated epoch because workers track the highest seen epoch and reject lower values.

### 7.3 Leader Election Security (Phase B)

* **Kubernetes lease:** Uses the Kubernetes API server's authentication and RBAC. Only Coordinator pods with the correct ServiceAccount can acquire the lease.
* **File lock:** Suitable only for single-host development. No authentication — relies on filesystem permissions.
* **Future embedded election:** Must include mutual authentication (mTLS) between Coordinator nodes. This is the same TLS infrastructure already planned for inter-node communication on port 4002.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | MetadataStore operations (Get/Set/Delete/PrefixScan/Snapshot), state reconstruction, fencing logic | Go `testing`, temp PebbleDB | 100% of MetadataStore interface |
| Integration Tests | Crash recovery round-trip: write state, kill process, restart, verify reconstruction | Go `testing`, in-process Coordinator | All job states (CREATED, DEPLOYING, RUNNING, FAILING) |
| Fencing Tests | Epoch validation: stale leader rejected, new leader accepted, epoch monotonicity | Go `testing`, mock workers | All fencing scenarios |
| Recovery Tests | Worker re-registration after Coordinator restart, task reconciliation | Go `testing`, mock workers | Match, orphaned, missing task cases |
| Performance Tests | PebbleDB write throughput, recovery time from 10K jobs, snapshot creation time | Go benchmarks | Recovery < 5s for 10K jobs |

### 8.1 Key Test Scenarios

1. **PebbleDB round-trip:** Write job metadata, close PebbleDB, reopen, verify all metadata is intact.
2. **Atomic batch:** Write a multi-key job submission batch. Kill the process mid-write (simulate with partial batch). Verify PebbleDB is consistent on reopen (all-or-nothing).
3. **Crash recovery:** Submit 100 jobs, start 50 running, complete 10 checkpoints. Kill Coordinator. Restart. Verify all 100 jobs, 50 running states, and 10 checkpoints are recovered.
4. **Worker reconciliation:** After recovery, simulate workers re-registering with various task states. Verify match, orphaned, and missing task handling.
5. **Fencing validation:** Simulate a stale Coordinator sending commands with old epoch. Verify workers reject them. Simulate new leader with higher epoch. Verify workers accept commands.
6. **Epoch persistence:** Start Coordinator (epoch 1). Crash. Restart. Verify epoch is 2 (incremented on recovery). New leader must not reuse epoch 1.
7. **PebbleDB snapshot:** Write 1000 jobs. Create snapshot. Verify snapshot directory contains a consistent copy. Open snapshot as a separate PebbleDB instance and verify all data.
8. **Heartbeat flush:** Write heartbeats in-memory. Trigger periodic flush. Kill Coordinator. Restart. Verify last heartbeat summary is available (up to 30s old).
9. **Checkpoint during crash:** Trigger checkpoint, write `CheckpointTriggered` to PebbleDB, kill before `CheckpointCompleted`. Restart. Verify the in-flight checkpoint is detected and aborted.
10. **LeaderElection interface:** Test with a mock election backend. Verify Campaign blocks until elected, returns correct epoch, and context is cancelled on leadership loss.

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should the FSM store full job graphs or only references (hashes) to graphs stored in an external blob store? Large job graphs (hundreds of operators) could bloat the metadata store. | Tarun | Open |
| 2 | How does the Coordinator HA interact with the discovery modes (`consul-kv`, `etcd-kv`, `dns`, `dns-srv`) currently commented out in `cmd/init.go`? Should Coordinators register themselves in a discovery service for worker bootstrapping? | Tarun | Open |
| 3 | When should Wire adopt embedded Raft? Evaluation criteria: (a) Coordinator is fully implemented with job submission, scheduling, and orchestration. (b) Deployment requirements demand sub-second failover without external supervisors. (c) PebbleDB snapshot replication latency is too high for acceptable recovery time. (d) The zero-dependency constraint eliminates external election backends. If all four criteria are met, implement Raft as a combined `LeaderElection` + `MetadataStore` backend. | Tarun | Open |
| 4 | What PebbleDB tuning is needed for Coordinator metadata vs worker state? Worker state backends use large MemTables (64 MB) and block caches (256 MB) for streaming throughput. Coordinator metadata is orders of magnitude smaller. Proposed: 4 MB MemTable, 8 MB block cache. Needs benchmarking to validate. | Tarun | Open |
| 5 | How should PebbleDB snapshots be transferred to standby Coordinators? Options: (a) Shared filesystem (NFS/EFS). (b) Direct transfer over the wire protocol (preferred — aligns with zero-dependency model). (c) Pebble's built-in checkpoint + rsync. Shared filesystem is simplest but adds an external dependency. | Tarun | Open |
| 6 | Risk: In Phase A (single-node), the Coordinator still requires an external supervisor (K8s/Systemd) to restart after a crash. This does not provide true HA. Acceptable as a stepping stone, but must be communicated clearly. | — | Acknowledged |
| 7 | Risk: PebbleDB WAL corruption after a crash could prevent recovery. Mitigation: periodic PebbleDB snapshots stored in a separate directory or remote storage. Snapshot frequency TBD. | — | Acknowledged |
| 8 | Risk: Leader election backends introduce an external dependency (Kubernetes API server, ZooKeeper, etc.). The file-lock backend is zero-dependency but single-host only. Wire may eventually need an embedded election protocol to satisfy the zero-dependency constraint for multi-node deployments. This is exactly where Raft re-enters the picture (Phase D). | — | Acknowledged |
