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
> **Last Updated:** `2026-02-22`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Wire's architecture document describes the Coordinator as "lightweight and generally stateless (relying on an external metadata store or leader election for HA)" but the HA mechanism itself is entirely unspecified. The operations document states that when the Coordinator crashes, "Workers lose heartbeat. Workers self-terminate. External Supervisor (K8s/Systemd) restarts Coordinator. Workers rejoin." This is a single-point-of-failure model: the Coordinator is the sole control plane node, and its loss halts all running jobs until an external process restarts it.

Wire's vision document declares the system must have "zero external dependencies (no ZooKeeper, no Etcd)." Yet the codebase already includes `hashicorp/raft` v1.7.1 and `hashicorp/yamux` v0.1.2 in `go.mod`, `cmd/init.go` contains 20+ Raft-specific configuration flags (heartbeat timeout, election timeout, leader lease, snapshot threshold, non-voter mode, node reaping, bootstrap-expect), and the TCP mux on port 4002 is already wired up for inter-node communication. The infrastructure for embedded Raft consensus exists in the dependency tree and configuration surface, but the actual Raft integration, the FSM, the failover protocol, and the metadata replication are undocumented and unimplemented.

Without Coordinator HA, Wire cannot claim production readiness. A single Coordinator crash during a checkpoint coordination window can leave jobs in an inconsistent state. A crash during task deployment can leave workers with orphaned tasks. There is no protocol for workers to discover a new leader, no mechanism for metadata to survive a Coordinator restart, and no defined behavior for split-brain scenarios.

### 1.2 Proposed Solution (Technical Summary)

Implement Coordinator High Availability using an embedded HashiCorp Raft consensus group. A cluster of 3 or 5 Coordinator nodes forms a Raft group where exactly one is elected Leader. The Leader Coordinator is the active control plane: it accepts job submissions, triggers checkpoints, manages task assignments, and coordinates failure recovery. Follower Coordinators are hot standbys that replicate all metadata via the Raft log but do not act on control plane decisions. If the Leader fails, Raft elects a new Leader from the followers, and the new Leader resumes all Coordinator responsibilities using the replicated metadata in its local FSM.

All Coordinator metadata -- job graphs, task assignments, checkpoint completion records, worker registrations, and cluster topology -- is stored as entries in the Raft log and materialized into a local finite state machine (FSM) backed by BoltDB (default) or BadgerDB (via `--store-db`). This FSM is the Coordinator's source of truth. Because it is replicated across all nodes in the Raft group, no external metadata store is required. The HA mechanism is fully embedded, consistent with Wire's zero-external-dependency philosophy.

Workers connect to all known Coordinator addresses and discover the current Leader via an HTTP redirect or a Raft leader-address query. On leader change, workers detect the new leader through heartbeat failures followed by re-discovery, and re-register with the new Leader. In-flight jobs survive failover because their metadata (job state, task assignments, latest completed checkpoint) is already replicated to the new Leader's FSM.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define embedded Raft consensus for Coordinator HA | Multi-region or geo-distributed HA |
| Specify metadata stored in the Raft FSM | Worker-side HA (workers are stateless executors) |
| Define failover protocol (leader change, worker re-registration) | Automatic Coordinator scaling (adding nodes to live cluster without downtime) |
| Define bootstrap process for new clusters | Data plane replication (Raft is control plane only) |
| Define node join/leave protocol | Hot standby that can serve read queries |
| Document split-brain and partition handling | External consensus system integration (etcd, ZooKeeper) |
| Specify non-voter (read-only) Coordinator nodes | Raft-based data stream replication |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Coordinator failover time | Infinite (manual restart) | < 5 seconds (Raft election) | Automated failover test |
| Job survival across failover | 0% (all jobs lost) | 100% of RUNNING jobs resume | Integration test |
| Metadata durability | None (in-memory only) | Survives any single-node failure | Chaos test |
| Zero external dependencies for HA | Violated (requires K8s/Systemd) | Fully embedded | Architecture review |
| Cluster bootstrap time (3 nodes) | N/A | < 30 seconds | Automated test |

---

## 2. Architecture & System Design

### 2.1 High-Level Architecture

```
                    ┌──────────────────────────────────────────┐
                    │           Raft Consensus Group            │
                    │                                          │
  ┌─────────────┐   │  ┌─────────────┐    ┌─────────────┐     │
  │   Worker 1  │───┼─>│ Coordinator │<──>│ Coordinator │     │
  └─────────────┘   │  │  (LEADER)   │    │ (FOLLOWER)  │     │
                    │  │             │    │             │     │
  ┌─────────────┐   │  │  Raft FSM   │    │  Raft FSM   │     │
  │   Worker 2  │───┼─>│  (BoltDB)   │    │  (BoltDB)   │     │
  └─────────────┘   │  └──────┬──────┘    └─────────────┘     │
                    │         │                                │
  ┌─────────────┐   │         │           ┌─────────────┐     │
  │   Worker 3  │───┼─>       │           │ Coordinator │     │
  └─────────────┘   │         │           │ (FOLLOWER)  │     │
                    │         │           │             │     │
                    │    Raft Log         │  Raft FSM   │     │
                    │    Replication ────>│  (BoltDB)   │     │
                    │                     └─────────────┘     │
                    └──────────────────────────────────────────┘

Port 4001: HTTP API (REST, health checks, metrics)
Port 4002: Raft consensus + Yamux internode transport
```

### 2.2 Component Breakdown

**Component 1:** Raft Consensus Engine

* **Responsibility:** Leader election, log replication, membership management, snapshotting.
* **Technology:** `hashicorp/raft` v1.7.1, embedded in the Coordinator process.
* **Interactions:** Communicates with peer Coordinators over TCP port 4002 via Yamux-multiplexed streams. Uses BoltDB or BadgerDB as the stable store and log store (configured via `--store-db`). Produces leadership change notifications consumed by the Coordinator's control loop.

**Component 2:** Coordinator FSM (Finite State Machine)

* **Responsibility:** Materializes Raft log entries into queryable metadata state. Serves as the single source of truth for all Coordinator decisions.
* **Technology:** Go struct implementing `hashicorp/raft.FSM` interface (`Apply`, `Snapshot`, `Restore`). Backed by BoltDB or BadgerDB on disk.
* **Interactions:** Receives committed log entries from the Raft engine. Queried by the Coordinator's job manager, checkpoint coordinator, and resource manager. Serialized to snapshots for new node catch-up.

**Component 3:** Raft Transport Layer

* **Responsibility:** Carries Raft RPCs (AppendEntries, RequestVote, InstallSnapshot) between Coordinator nodes.
* **Technology:** `hashicorp/raft.NetworkTransport` over TCP connections multiplexed by `hashicorp/yamux` on port 4002. The existing TCP mux (`internal/tcp/mux.go`) manages Yamux sessions, with Raft traffic carried on dedicated logical streams.
* **Interactions:** Listens on `--raft-addr` (default `localhost:4002`). Advertises `--raft-adv-addr` for NAT/container environments. Supports TLS via `--node-cert`, `--node-key`, `--node-ca-cert`.

**Component 4:** Leader Coordinator (Active Control Plane)

* **Responsibility:** The sole node that executes control plane logic: accepting job submissions, scheduling tasks, triggering checkpoints, detecting worker failures, orchestrating recovery.
* **Technology:** Standard Coordinator logic, activated only when `raft.State() == Leader`.
* **Interactions:** Receives worker heartbeats and RPC calls. Writes all state mutations (job state transitions, task assignments, checkpoint completions) as Raft log entries. Only proceeds after the entry is committed (majority replicated).

**Component 5:** Follower Coordinator (Hot Standby)

* **Responsibility:** Replicates all metadata from the Leader via Raft log application. Remains idle (no control plane actions) until promoted to Leader.
* **Technology:** Same Coordinator binary, but control loops are gated behind a leadership check.
* **Interactions:** Applies FSM updates passively. Redirects any client HTTP requests to the current Leader. Can serve read-only status queries (e.g., `GET /api/v1/cluster`) from its local FSM if stale reads are acceptable.

### 2.3 Data Flow

**Normal Operation (Leader healthy):**

1. Worker sends heartbeat to Leader Coordinator (HTTP or RPC on port 4001).
2. Leader processes heartbeat, updates worker last-seen timestamp in FSM via Raft log entry.
3. Raft replicates the log entry to Followers. Followers apply it to their local FSMs.
4. Leader triggers checkpoint: writes `CheckpointTriggered{ID: N}` to Raft log.
5. Workers complete checkpoint and ACK to Leader.
6. Leader writes `CheckpointCompleted{ID: N, Offsets: [...]}` to Raft log.
7. All FSMs now reflect the latest completed checkpoint.

**Failover (Leader crashes):**

1. Followers detect missing heartbeats from Leader (after `--raft-timeout`, default 1s).
2. Followers transition to Candidate state after `--raft-election-timeout` (default 1s).
3. A Candidate wins election with majority votes. Becomes new Leader.
4. New Leader reads FSM to reconstruct full cluster state: all jobs, task assignments, worker registrations, latest completed checkpoints.
5. New Leader begins accepting worker heartbeats.
6. Workers detect Leader loss (heartbeat/RPC failures), query known Coordinator addresses for new Leader, re-register.
7. New Leader reconciles: any in-flight checkpoint that was not marked `Completed` in the FSM is aborted. A new checkpoint is triggered to establish a clean recovery point.
8. Jobs continue from the last completed checkpoint without resubmission.

---

## 3. API Design

### 3.1 Coordinator Failover Protocol

#### 3.1.1 Leader Discovery

Workers and external clients discover the current Leader through one of two mechanisms:

**Mechanism A: HTTP Redirect**

Any Coordinator node (Leader or Follower) exposes the HTTP API on port 4001. If a Follower receives a write request (job submission, cancel, etc.), it responds with:

```
HTTP/1.1 301 Moved Permanently
Location: http://<leader-http-addr>/api/v1/jobs
X-Wire-Leader-Id: node-1
X-Wire-Leader-Addr: node1:4001
```

Read-only requests (`GET /api/v1/cluster`, `GET /healthz`) may be served locally by the Follower if `--allow-stale-reads` is enabled (not yet implemented; see Open Questions).

**Mechanism B: Leader Address Query**

```
GET /api/v1/cluster/leader
```

**Response (200 OK) -- served by any node:**

```json
{
  "leader_id": "node-1",
  "leader_http_addr": "node1:4001",
  "leader_raft_addr": "node1:4002",
  "is_self": false
}
```

Workers cache the Leader address and use it for all subsequent RPCs. On connection failure, workers re-query any known Coordinator address.

#### 3.1.2 Worker Re-registration After Failover

When a worker detects that its heartbeat or RPC calls to the Leader are failing:

1. Worker enters a **leader-discovery loop**: iterates through its list of known Coordinator addresses (provided via `--join` or discovery), calling `GET /api/v1/cluster/leader` on each.
2. Once a new Leader is found, the worker sends a `RegisterWorker` RPC containing:
   - `worker_id`: Stable identifier for this worker.
   - `task_slots_total`: Number of task slots available.
   - `running_tasks`: List of task IDs currently executing on this worker.
3. The new Leader compares the worker's reported running tasks against the FSM's task assignment records. Three outcomes:
   - **Match:** Task is assigned to this worker in the FSM. No action needed.
   - **Orphaned task:** Worker reports a task not in the FSM. Leader instructs worker to cancel it.
   - **Missing task:** FSM shows a task assigned to this worker, but worker does not report it. Leader marks the task as FAILED and initiates recovery.

#### 3.1.3 Job Survival Across Failover

Jobs survive Coordinator failover because all job metadata is replicated in the Raft FSM:

- **RUNNING jobs:** The new Leader finds the job in RUNNING state in the FSM. It waits for workers to re-register and reconcile task assignments. If all tasks are accounted for, the job continues without interruption.
- **DEPLOYING jobs:** The new Leader finds the job in DEPLOYING state. It re-issues task deployment commands to workers. If workers already received and started the tasks, the reconciliation in 3.1.2 handles the overlap.
- **FAILING jobs:** The new Leader picks up the recovery workflow. It selects the latest completed checkpoint from the FSM and re-deploys tasks.
- **In-flight checkpoints:** Any checkpoint that was triggered but not completed at the time of failover is aborted. The new Leader triggers a fresh checkpoint to establish a clean baseline.

#### 3.1.4 Bootstrap Process for New Cluster

A new Wire cluster is bootstrapped using the `--bootstrap-expect` flag:

1. Start N Coordinator nodes (recommended: 3 or 5), each with `--bootstrap-expect=N` and `--join=<addr1>,<addr2>,...,<addrN>`.
2. Each node attempts to join the addresses listed in `--join`. Join attempts repeat `--join-attempts` times (default 5) with `--join-interval` (default 3s) delay.
3. Once a node has discovered `N` peers (including itself), it initiates the Raft bootstrap. The first node to achieve quorum becomes the initial Leader.
4. The bootstrap process must complete within `--bootstrap-expect-timeout` (default 120s). If it does not, the node exits with an error.
5. After bootstrap, the Raft cluster is fully formed. Workers can now connect to any Coordinator address and discover the Leader.

**Single-node mode:** When `--bootstrap-expect` is 0 (default) and `--join` is empty, the Coordinator bootstraps as a single-node Raft cluster. It is immediately the Leader. This is suitable for development and testing but provides no HA.

#### 3.1.5 Node Join Protocol

**Adding a Coordinator to an existing cluster:**

1. Start a new Coordinator node with `--join=<existing-leader-addr>`.
2. The new node sends a join request to the Leader's HTTP API:
   ```
   POST /api/v1/cluster/nodes
   {
     "node_id": "node-4",
     "raft_addr": "node4:4002",
     "voter": true
   }
   ```
3. The Leader adds the new node to the Raft configuration as a Voter (or NonVoter if `--raft-non-voter` is set).
4. Raft replicates the current log and snapshots to the new node. Once caught up, the node becomes a full member.

**Removing a Coordinator from an existing cluster:**

1. Graceful removal: If `--raft-cluster-remove-shutdown` is true, the node removes itself from the Raft configuration before shutting down. If `--raft-shutdown-stepdown` is true and the node is the Leader, it steps down first, triggering a new election.
2. Forced removal: An operator calls `DELETE /api/v1/cluster/nodes/{node_id}` on the Leader. The Leader removes the node from the Raft configuration.
3. Automatic reaping: If `--raft-reap-node-timeout` is set (e.g., `72h`), any voting node that has been unreachable for that duration is automatically removed from the Raft configuration by the Leader. Non-voting nodes are reaped after `--raft-reap-read-only-node-timeout`.

#### 3.1.6 Node Join Authentication

Join requests can be authenticated using the `--join-as` flag, which specifies a username in the auth file (`--auth`). The joining node includes credentials in the join request. The Leader validates the credentials against the auth file before admitting the node. If `--join-as` is not set, joins are anonymous (suitable for trusted networks).

---

## 4. Data Model & Storage

### 4.1 Raft FSM Schema

The FSM is the materialized view of the Raft log. It stores all metadata needed for the Coordinator to operate. The FSM is backed by BoltDB (default) or BadgerDB, selected via `--store-db`.

**Bucket/Namespace: `jobs`**

| Key | Type | Description |
| -- | -- | -- |
| `jobs/{job_id}/meta` | JobMeta | Core job metadata (name, status, parallelism, config hash) |
| `jobs/{job_id}/graph` | []byte | Serialized optimized JobGraph (operator chain, shuffle edges) |
| `jobs/{job_id}/assignments` | TaskAssignmentMap | Mapping of task_id to worker_id for all parallel task instances |
| `jobs/{job_id}/checkpoints/latest` | int64 | ID of the latest completed checkpoint |
| `jobs/{job_id}/checkpoints/{cp_id}` | CheckpointMeta | Metadata for a specific checkpoint (offsets, state paths, timestamp) |
| `jobs/{job_id}/savepoints/{sp_id}` | SavepointMeta | Savepoint metadata (path, status, trigger time) |

**Bucket/Namespace: `workers`**

| Key | Type | Description |
| -- | -- | -- |
| `workers/{worker_id}/meta` | WorkerMeta | Worker registration (address, task slots total/available, last heartbeat) |
| `workers/{worker_id}/tasks` | []string | List of task IDs currently assigned to this worker |

**Bucket/Namespace: `cluster`**

| Key | Type | Description |
| -- | -- | -- |
| `cluster/config` | ClusterConfig | Global cluster configuration (checkpoint interval, default parallelism) |
| `cluster/nodes` | []NodeInfo | Raft cluster membership (node ID, address, voter/non-voter status) |

### 4.2 Raft Log Entry Types

All mutations to the FSM are submitted as Raft log entries. Each entry has a type discriminator and a msgpack-serialized payload (using `hashicorp/go-msgpack`).

```go
type LogEntryType uint8

const (
    LogJobSubmitted       LogEntryType = iota + 1  // Job created, graph stored
    LogJobStateChanged                              // Status transition (DEPLOYING, RUNNING, etc.)
    LogTaskAssigned                                 // Task assigned to worker
    LogTaskStateChanged                             // Task status update
    LogCheckpointTriggered                          // Checkpoint initiated
    LogCheckpointCompleted                          // Checkpoint completed with offsets
    LogCheckpointFailed                             // Checkpoint failed
    LogWorkerRegistered                             // Worker joined cluster
    LogWorkerDeregistered                           // Worker removed (graceful or reaped)
    LogWorkerHeartbeat                              // Worker heartbeat (last-seen update)
    LogSavepointTriggered                           // Savepoint initiated
    LogSavepointCompleted                           // Savepoint completed
    LogClusterConfigChanged                         // Global config update
)
```

### 4.3 Raft Snapshot Format

When the Raft log grows beyond `--raft-snap` entries (default 8192), the Leader creates a snapshot of the FSM. The snapshot is a full serialization of all buckets/namespaces in the FSM store.

The snapshot is written as a length-prefixed msgpack stream:

```
[4 bytes: version][4 bytes: bucket count]
For each bucket:
  [4 bytes: bucket name length][N bytes: bucket name]
  [4 bytes: entry count]
  For each entry:
    [4 bytes: key length][N bytes: key]
    [4 bytes: value length][N bytes: value]
```

The snapshot interval is checked every `--raft-snap-int` (default 10s). Snapshots are stored in the `snapshots/` subdirectory under `--raft-dir`.

### 4.4 Storage Considerations

* **Data volume:** FSM size is proportional to the number of active jobs, tasks, and retained checkpoints. For a cluster with 100 jobs, 1000 tasks, and 10 checkpoints per job, the FSM is approximately 10-50 MB.
* **Log compaction:** Raft snapshots automatically compact the log. After a snapshot, all preceding log entries are discarded.
* **Disk requirements:** Each Coordinator node needs storage for the Raft log (bounded by snapshot threshold), the latest snapshot, and the FSM database. Recommended minimum: 1 GB for the Raft directory.
* **Write amplification:** BoltDB uses B+ tree with copy-on-write. BadgerDB uses an LSM tree. For write-heavy workloads (frequent heartbeats), BadgerDB may offer better write throughput. For read-heavy workloads (job status queries), BoltDB may be preferable.

---

## 5. Design Decisions & Trade-offs

### Decision 1: Embedded Raft (not external etcd or ZooKeeper)

|  |  |
| -- | -- |
| **Context** | Wire's vision mandates "zero external dependencies (no ZooKeeper, no Etcd)." The Coordinator needs HA with consistent metadata replication. |
| **Options Considered** | (A) Embedded HashiCorp Raft, (B) External etcd cluster, (C) External ZooKeeper ensemble, (D) Embedded etcd (via embed package) |
| **Decision** | Option A: Embedded HashiCorp Raft |
| **Rationale** | Raft is already a dependency (`hashicorp/raft` v1.7.1 in go.mod). Embedded Raft keeps the single-binary deployment model. No external process to operate, monitor, or version-manage. etcd's embed package pulls in a massive dependency tree and is designed for a different use case (general-purpose KV store). ZooKeeper requires a JVM. HashiCorp Raft is battle-tested in Consul, Nomad, and Vault. |
| **Trade-offs Accepted** | Wire must implement its own FSM and snapshot logic. No external tooling for inspecting metadata (unlike etcd's `etcdctl`). Raft group size is limited to the Coordinator cluster (typically 3-5 nodes). |
| **Revisit Trigger** | If metadata query patterns become complex enough to warrant a full KV store with watches and transactions. |

### Decision 2: BoltDB as default Raft store (with BadgerDB option)

|  |  |
| -- | -- |
| **Context** | Raft needs a stable store (current term, voted-for) and a log store (committed entries). Both must survive process restarts. |
| **Options Considered** | (A) BoltDB only, (B) BadgerDB only, (C) BoltDB default with BadgerDB option via `--store-db` |
| **Decision** | Option C: BoltDB default, BadgerDB optional |
| **Rationale** | BoltDB (`go.etcd.io/bbolt`) is the standard choice for HashiCorp Raft. It is simple, well-tested, and has low operational overhead. BadgerDB (`dgraph-io/badger`) offers better write throughput for high-frequency updates (e.g., worker heartbeats). Both are already in `go.mod`. The `--store-db` flag provides flexibility without committing to one. `rqlite/raft-boltdb` provides the BoltDB adapter for HashiCorp Raft's `LogStore` and `StableStore` interfaces. |
| **Trade-offs Accepted** | Two code paths to maintain and test. BoltDB has higher write amplification under heavy write loads. BadgerDB has higher memory usage and more complex tuning. |
| **Revisit Trigger** | If benchmarks show one backend is strictly superior for Wire's workload, eliminate the other. |

### Decision 3: Leader-only writes (no multi-leader)

|  |  |
| -- | -- |
| **Context** | Control plane operations (job submissions, checkpoint coordination) must be consistent. |
| **Options Considered** | (A) Leader-only writes with follower redirect, (B) Multi-leader with conflict resolution, (C) Leaderless with CRDTs |
| **Decision** | Option A: Leader-only writes |
| **Rationale** | Raft inherently provides linearizable writes through a single Leader. This is the simplest correct approach. Multi-leader adds conflict resolution complexity that is unnecessary for a control plane. The Coordinator's write rate is low (job submissions, checkpoint completions) -- a single Leader is not a bottleneck. |
| **Trade-offs Accepted** | All writes must pass through the Leader, adding one network hop for clients that connect to a Follower. Leader becomes a bottleneck only if write rate exceeds Raft throughput (unlikely for metadata). |
| **Revisit Trigger** | If control plane write rate exceeds 10,000 ops/sec (extremely unlikely for metadata). |

### Decision 4: Heartbeats in the Raft log (not out-of-band)

|  |  |
| -- | -- |
| **Context** | Worker heartbeats update the `last_seen` timestamp in the FSM. This could be done via Raft (replicated) or out-of-band (local only). |
| **Options Considered** | (A) Heartbeats as Raft log entries, (B) Out-of-band heartbeats stored only on Leader, (C) Hybrid: periodic Raft summary + frequent out-of-band |
| **Decision** | Option C: Hybrid approach |
| **Rationale** | Raw heartbeats (every 1-5 seconds per worker) would flood the Raft log. Instead, workers send heartbeats directly to the Leader over HTTP/RPC (out-of-band). The Leader maintains an in-memory `last_seen` map. Periodically (every 30s), the Leader writes a `WorkerHealthSummary` Raft log entry that snapshots the liveness state. On failover, the new Leader has a recent-enough view of worker health and can quickly re-establish liveness through worker re-registration. |
| **Trade-offs Accepted** | Up to 30 seconds of heartbeat data can be lost on failover. The new Leader must wait for workers to re-register before it has full liveness information. |
| **Revisit Trigger** | If 30 seconds of staleness causes incorrect failure detection after failover. |

### Decision 5: Yamux-multiplexed Raft transport (not separate TCP connections)

|  |  |
| -- | -- |
| **Context** | Raft needs a reliable transport between Coordinator nodes. Wire already uses Yamux for inter-node communication on port 4002. |
| **Options Considered** | (A) Yamux streams on port 4002 (shared with other internode traffic), (B) Dedicated TCP port for Raft, (C) gRPC transport for Raft |
| **Decision** | Option A: Yamux streams on port 4002 |
| **Rationale** | The TCP mux (`internal/tcp/mux.go`) already manages Yamux sessions between nodes. Raft traffic is low-bandwidth metadata. Sharing the port simplifies firewall rules and deployment. HashiCorp Raft's `NetworkTransport` accepts any `net.Listener`, so the Yamux mux's `Accept()` method can serve as the Raft listener. A byte prefix or stream type header distinguishes Raft streams from data streams. |
| **Trade-offs Accepted** | Raft traffic shares bandwidth with other internode traffic. If data plane traffic saturates the connection, Raft heartbeats could be delayed, causing spurious elections. |
| **Revisit Trigger** | If Raft election instability is observed under high internode data traffic. Mitigate by prioritizing Raft streams in the Yamux config or moving to a dedicated port. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | **Split brain: network partition isolates Leader from majority** | The isolated Leader cannot commit new log entries (no majority). It steps down after `--raft-leader-lease-timeout` expires. The majority partition elects a new Leader. Workers on the minority side fail to reach either Leader and enter re-discovery. When the partition heals, the old Leader rejoins as a Follower, and its uncommitted log entries are discarded. | Brief control plane unavailability (seconds). No data loss for committed entries. | High |
| 2 | **Network partition splits Coordinators 2-1 with Workers on the minority side** | The 2-node majority partition has a Leader but the workers cannot reach it. Workers on the minority side self-terminate after heartbeat timeout. When the partition heals, workers reconnect and re-register. The Leader redeploys tasks to available workers. | Jobs on partitioned workers fail and recover from checkpoint. | High |
| 3 | **Leader election during in-flight checkpoint** | The old Leader triggered `CheckpointTriggered{ID: N}` but the entry may or may not have been committed. Case A: If committed, the new Leader sees it in the FSM and waits for worker ACKs. If ACKs never arrive (because workers lost the old Leader), the new Leader times out the checkpoint and triggers a new one. Case B: If not committed, the new Leader never sees it. Workers that started snapshotting will time out and discard the partial checkpoint. The new Leader triggers a fresh checkpoint. | One checkpoint lost. Next checkpoint succeeds normally. | Medium |
| 4 | **Leader crashes while applying a job submission** | The Raft entry for `LogJobSubmitted` was either committed or not. If committed, the new Leader sees the job in CREATED state and can proceed. If not committed, the entry is lost. The client receives no response (connection reset) and must retry. The retry is safe because job submission is idempotent (keyed by job name). | Client must retry. No inconsistency. | Medium |
| 5 | **All Coordinator nodes crash simultaneously** | Raft cannot elect a Leader. Jobs halt. On restart, each Coordinator loads its persisted Raft state (log + snapshots) from disk. The first to form a majority quorum elects a Leader. FSM is restored from the last snapshot + unapplied log entries. Workers that survived reconnect and re-register. | Full control plane outage until quorum restored. Job state preserved on disk. | Critical |
| 6 | **New Coordinator joins with stale/corrupted data directory** | Raft detects log inconsistency during replication. The Leader sends a full snapshot via `InstallSnapshot` RPC. The joining node discards its stale FSM and rebuilds from the snapshot. | Brief delay while snapshot transfers. No data loss. | Low |
| 7 | **Follower falls behind by more than snapshot threshold** | The Leader's log has been compacted (old entries removed after snapshot). The Follower cannot catch up via log replication alone. The Leader sends its latest snapshot to the Follower. The Follower rebuilds its FSM from the snapshot and continues normal replication. | Temporary increased network usage during snapshot transfer. | Low |
| 8 | **Worker connects to Follower and sends a write request** | The Follower does not process the write. It returns an HTTP 301 redirect to the Leader's address. The worker retries at the Leader. | One additional round trip. | Low |
| 9 | **Leader lease expires due to GC pause or CPU starvation** | The Leader steps down. A new election occurs. The old Leader (if still running) discovers it is no longer Leader and stops processing. Any writes it attempted during the lease gap were not committed (no majority ACK) and are discarded. | Brief election. No split-brain because lease prevents stale Leader from committing. | Medium |
| 10 | **Bootstrap: fewer than `--bootstrap-expect` nodes start within timeout** | The bootstrap process fails. Each node logs an error and exits. Operator must investigate and restart. | Cluster does not form. No data loss (nothing was committed). | Medium |

---

## 7. Security & Compliance

### 7.1 Raft Transport Encryption

* **TLS for inter-Coordinator communication:** When `--node-cert` and `--node-key` are provided, all Raft traffic on port 4002 is encrypted using TLS 1.3 (enforced in `internal/tcp/mux.go`'s `NewTLSMux`). This covers AppendEntries, RequestVote, InstallSnapshot, and all other Raft RPCs.
* **Mutual TLS:** When `--node-verify-client` is enabled, Coordinator nodes mutually authenticate each other using X.509 certificates. This prevents unauthorized nodes from joining the Raft group.
* **Certificate verification:** The `--node-ca-cert` flag specifies the CA certificate for verifying peer certificates. The `--node-verify-server-name` flag specifies the expected hostname on peer certificates. If `--node-no-verify` is true, certificate verification is skipped (development only).

### 7.2 Join Authentication

* **Authenticated joins:** The `--join-as` flag enables authenticated cluster joins. The joining node must present valid credentials (username from the `--auth` file). The Leader validates credentials before adding the node to the Raft configuration.
* **Unauthenticated joins:** When `--join-as` is not set, any node that can reach the Leader on the Raft port can join. This is acceptable in trusted networks but should be disabled in production by setting `--auth` and `--join-as`.

### 7.3 FSM Data Protection

* **Encryption at rest:** The Raft log and FSM database (BoltDB/BadgerDB) are stored in `--raft-dir`. Encryption at rest depends on the underlying filesystem or volume encryption (e.g., LUKS, dm-crypt, EBS encryption). Wire does not implement application-level encryption for the FSM.
* **Sensitive metadata:** Job configurations stored in the FSM may reference connector credentials via `${ENV_VAR}` substitution. The resolved values are never stored in the FSM -- only the variable references. Actual credentials are resolved at runtime on the worker.

### 7.4 Raft Log Integrity

* Raft's own protocol guarantees log integrity: entries are committed only when a majority of nodes have persisted them. BoltDB and BadgerDB both use checksums on their data files. Corruption of a single node's log is detected and corrected via snapshot transfer from the Leader.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | FSM Apply/Snapshot/Restore, log entry serialization, leader discovery logic | Go `testing`, mock Raft | 100% of FSM operations |
| Integration Tests | 3-node Raft cluster lifecycle: bootstrap, failover, re-election, node join/leave | Go `testing`, in-process Raft cluster | All state transitions |
| Chaos Tests | Leader kill during checkpoint, network partition simulation, disk full | Docker Compose + `tc` (traffic control) | All edge cases in Section 6 |
| Performance Tests | Raft throughput under sustained metadata writes, failover latency measurement | Go benchmarks, custom harness | Failover < 5s, throughput > 1000 ops/sec |

### 8.1 Key Test Scenarios

1. **Bootstrap:** Start 3 Coordinator nodes with `--bootstrap-expect=3`. Verify one becomes Leader within 30s. Verify all FSMs are consistent.
2. **Leader failover:** Kill the Leader process. Verify a new Leader is elected within 5s. Verify all job metadata is preserved in the new Leader's FSM.
3. **Job survival:** Submit a job, start it running, kill the Leader. Verify the new Leader picks up the job in RUNNING state and workers re-register without job restart.
4. **Checkpoint during failover:** Trigger a checkpoint, kill the Leader before `CheckpointCompleted` is written. Verify the new Leader aborts the in-flight checkpoint and triggers a new one.
5. **Worker re-registration:** After failover, verify all workers re-discover the new Leader and re-register within 30s. Verify task assignment reconciliation produces correct results.
6. **Node join:** Add a 4th Coordinator to a running 3-node cluster. Verify it receives the snapshot and catches up. Verify it can become Leader if the current Leader is killed.
7. **Node removal:** Gracefully shut down a Coordinator with `--raft-cluster-remove-shutdown`. Verify the Raft configuration shrinks. Verify the cluster continues to operate with the remaining nodes.
8. **Network partition:** Simulate a partition isolating the Leader. Verify the majority partition elects a new Leader. Verify the old Leader steps down. Verify cluster reconverges when the partition heals.
9. **Split-brain safety:** During a partition, attempt writes on both sides. Verify only the majority-side Leader can commit writes. Verify no committed data is lost when the partition heals.
10. **Snapshot and restore:** Fill the FSM with 1000 jobs. Trigger a snapshot. Add a new node. Verify it receives the snapshot and has all 1000 jobs in its FSM.
11. **TLS verification:** Start a 3-node cluster with mutual TLS. Attempt to join a node with an invalid certificate. Verify the join is rejected.

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should Follower Coordinators serve read-only queries (e.g., job status, cluster info) from their local FSM, or should all reads go to the Leader? Stale reads improve availability but may return outdated data. | Tarun | Open |
| 2 | What is the optimal frequency for the hybrid heartbeat summary (Decision 4)? 30 seconds may be too long if a failover occurs right before the summary. 5 seconds may add too much Raft log traffic. | Tarun | Open |
| 3 | Should Wire support Raft observer nodes (non-voting, non-promoting) for monitoring or geographic read replicas? The `--raft-non-voter` flag exists but its use case is undefined. | Tarun | Open |
| 4 | How should the Coordinator handle the transition from single-node mode (development) to a multi-node cluster? Is there a migration path, or must the user bootstrap a new cluster and re-submit jobs? | Tarun | Open |
| 5 | Should the FSM store full job graphs or only references (hashes) to graphs stored in an external blob store? Large job graphs (hundreds of operators) could bloat the Raft log. | Tarun | Open |
| 6 | Risk: The Yamux-shared port for Raft traffic (Decision 5) may cause election instability under heavy data plane load. Need benchmarks to validate. If problematic, a dedicated Raft port is the fallback. | Tarun | Acknowledged |
| 7 | Risk: BoltDB's single-writer lock may become a bottleneck if worker heartbeat summaries and checkpoint completions are written frequently. BadgerDB's concurrent writes may help, but need benchmarks. | Tarun | Acknowledged |
| 8 | How does the Coordinator HA interact with the discovery modes (`consul-kv`, `etcd-kv`, `dns`, `dns-srv`) currently commented out in `cmd/init.go`? Should Coordinators register themselves in a discovery service for worker bootstrapping? | Tarun | Open |
| 9 | What is the maximum recommended cluster size for the Coordinator Raft group? HashiCorp recommends 3 or 5 nodes. Should Wire enforce a maximum? | Tarun | Open |
| 10 | Risk: If all Coordinators are co-located in a single availability zone, a zone failure defeats HA. Should the documentation recommend cross-AZ deployment, or should Wire enforce it? | — | Acknowledged |
