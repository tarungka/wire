# Wire vs. Apache Flink: Documentation & Design Comparison

**Status:** Reference
**Version:** 0.1.0
**Context:** Comparative analysis of Wire's canonical docs against Apache Flink's official documentation

---

## Purpose

Side-by-side comparison identifying where Wire mirrors Flink, where it diverges, and what each covers that the other doesn't. Based on Flink's docs at `nightlies.apache.org/flink/flink-docs-master/`.

---

## 1. Architecture: Cluster Topology

### Flink
- **JobManager** (control plane) with 3 sub-components: ResourceManager, Dispatcher, JobMaster
- **TaskManagers** (data plane) execute tasks in **Task Slots**
- **Client** is a separate component that compiles apps into JobGraphs and submits them
- JobManager is a complex, multi-role coordinator — ResourceManager handles YARN/K8s/Standalone resource provisioning, Dispatcher provides REST + WebUI, JobMaster manages a single job's lifecycle
- Supports **multiple concurrent jobs** via separate JobMaster instances

### Wire
- **Coordinator** (control plane) — described as "lightweight, generally stateless"
- **Workers** (data plane) execute tasks in **Task Slots**
- No separate Client component documented
- Coordinator is monolithic — combines job management, checkpoint coordination, resource tracking, failure recovery in one role
- No mention of multi-job support

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Control plane granularity | 3 sub-components (ResourceManager, Dispatcher, JobMaster) | Single monolithic Coordinator |
| Client | Separate component with CLI, REST, SQL Client | Not documented |
| Multi-job | Yes, separate JobMaster per job | Not specified |
| REST API / WebUI | Built into Dispatcher | Not documented |
| HA | Leader/Standby with ZooKeeper or K8s | Not specified (no consensus mechanism documented) |

**Gap for Wire:** Flink's separation of ResourceManager/Dispatcher/JobMaster is a mature pattern. Wire's monolithic coordinator will need to be decomposed as it grows. The lack of a Client component means Wire has no documented job submission API.

---

## 2. Task Execution & Operator Chaining

### Flink
- Chains consecutive operators into a single **Task** executed by one thread
- **Task Slots** are fixed resource subsets of a TaskManager (managed memory partitioning)
- **Slot Sharing** — subtasks from the same job share slots (cluster needs slots = max parallelism, not sum)
- No CPU isolation within slots (only memory)
- Graph pipeline: Logical Graph → JobGraph → Physical Graph (ExecutionGraph)

### Wire
- Also chains operators — "fuses sequential operators into single Goroutine to avoid serialization overhead"
- Each Task Slot has a dedicated Pebble directory for state isolation
- Graph pipeline: StreamGraph → JobGraph → ExecutionGraph (identical to Flink)
- No mention of slot sharing

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Execution unit | Thread per task | Goroutine per task |
| Slot sharing | Yes (major resource optimization) | Not documented |
| State isolation | Shared RocksDB instance per TaskManager | Dedicated Pebble instance per Task Slot |
| CPU isolation | None | Not specified |

**Wire's advantage:** Dedicated Pebble per slot gives stronger isolation (no "noisy neighbor" at the DB level). Flink shares RocksDB across slots.

**Wire's gap:** No slot sharing means Wire may require more slots than necessary.

---

## 3. State Management

### Flink
- **3 state backend options:** HashMapStateBackend (in-memory), EmbeddedRocksDBStateBackend (disk), ForStStateBackend (experimental, disaggregated to remote FS)
- State backends are **pluggable** at configuration time
- RocksDB is the only backend supporting **incremental checkpoints**
- Flink manages RocksDB memory automatically (shared cache, write buffer manager)
- **Keyed State** types: ValueState, ListState, MapState, ReducingState, AggregatingState
- **Key Groups** as the atomic unit for state redistribution during rescaling
- **Changelog feature** — uploads state changes continuously for lower checkpoint latency

### Wire
- **Single backend: Pebble** (with `StateBackend` interface for future alternatives)
- Interface: `Put`, `Get`, `Delete`, `NewIterator(prefix)`, `Checkpoint`, `Restore`
- Keyed State types: ValueState, ListState, MapState (subset of Flink's)
- Key Groups with KeyGroupPrefix encoding for rescaling (mirrors Flink)
- No changelog/continuous upload feature
- Memory management: recommends "Pebble Block Cache: 30-40% of Worker RAM"

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Backend options | 3 (HashMap, RocksDB, ForSt) | 1 (Pebble) + interface for future |
| In-memory backend | Yes (HashMapStateBackend) | No |
| Incremental checkpoints | Only with RocksDB | Yes (Pebble hard-link snapshots) |
| State types | 5 types | 3 types (missing ReducingState, AggregatingState) |
| Auto memory management | Yes (RocksDB memory bounded to managed memory) | Manual recommendation only |
| Changelog | Yes (continuous upload) | No |
| Disaggregated state | ForSt (experimental) | No |

**Wire's advantage:** Pebble's hard-link snapshots are near-zero-cost (milliseconds). Flink's RocksDB incremental checkpoints are more complex and slower.

**Wire's gap:** No in-memory backend for simple/small-state jobs. Missing ReducingState and AggregatingState. No changelog feature.

---

## 4. Checkpointing & Fault Tolerance

### Flink
- **Barrier-based snapshotting** (Chandy-Lamport variant)
- Stream barriers injected into data, flow with records
- **Barrier alignment** for multi-input operators (buffer until all barriers arrive)
- **Unaligned checkpointing** — barriers can overtake in-flight data (faster propagation in backpressured pipelines, but higher I/O)
- **Exactly-once vs At-least-once** — configurable per job (skip alignment for lower latency)
- **Concurrent snapshots** — multiple checkpoint barriers can coexist in the stream
- Checkpoint storage: JobManager heap or filesystem (HDFS/S3)
- Directory structure: `/{job-id}/shared/`, `taskowned/`, `chk-N/`

### Wire
- **Asynchronous Barrier Snapshot** (Chandy-Lamport variant) — same algorithm
- Barrier alignment with buffering for multi-input operators — same approach
- No unaligned checkpointing
- Only exactly-once (no configurable at-least-once mode)
- No mention of concurrent snapshots
- Checkpoint storage: S3/MinIO
- Directory structure: `s3://bucket/jobs/<job-id>/checkpoints/chk-N/`

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Core algorithm | Chandy-Lamport ABS | Chandy-Lamport ABS (same) |
| Unaligned checkpoints | Yes | No |
| Exactly-once vs at-least-once | Configurable | Exactly-once only |
| Concurrent snapshots | Yes | Not documented |
| Local checkpoint storage | Yes (JobManager heap) | No (always remote S3/MinIO) |

**Wire's simplification:** Only supporting exactly-once is a design choice that reduces complexity. Flink added at-least-once mode for latency-sensitive jobs — Wire may need this later.

**Wire's gap:** No unaligned checkpointing. This matters for backpressured pipelines — aligned checkpoints can take very long when a slow operator blocks barrier propagation. This is a significant production concern.

---

## 5. Savepoints

### Flink
- **Manually triggered checkpoints** that persist indefinitely
- Used for: planned upgrades, parallelism changes, A/B testing, forking jobs
- **Operator UIDs** — users assign stable IDs so savepoint state maps correctly across code changes
- **Two formats:** Canonical (portable, slow) and Native (fast, backend-specific)
- **Claim modes:** NO_CLAIM (user owns files), CLAIM (Flink manages lifecycle)
- Can resume with `-n` flag to skip non-restored state (for removed operators)

### Wire
- **Savepoints** mentioned in operations doc only in context of rescaling ("Trigger manual Savepoint")
- No operator UID system
- No canonical vs native format distinction
- No claim mode semantics

**Wire's gap:** Savepoints are a critical operational feature in Flink. Wire barely documents them. The operator UID system is essential for production — without it, any code change can break state restoration.

---

## 6. Time & Watermarks

### Flink
- **Event Time** and **Processing Time** (two notions)
- Watermarks: special markers flowing in streams, declaring "event time has reached T"
- Parallel watermark generation — each source subtask generates independently
- Multi-input operators: event time = minimum of input watermarks
- **Late data handling** via `allowedLateness` window configuration
- Windows: Tumbling, Sliding, Session (+ count-based triggers)

### Wire
- **Event Time** and **Processing Time** (same two notions)
- Watermarks: "Control packets declaring no more events with timestamp < T will arrive"
- Generated by Source Connectors, propagated as `OutputWatermark = Min(InputWatermarks)`
- Late data: "dropped by default or configurable grace period"
- Windows: Tumbling, Sliding, Session
- Window state scoped to `(Key, WindowID)` with auto-cleanup

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Core semantics | Event Time + Processing Time | Same |
| Watermark generation | Per-subtask parallel | Per-source connector |
| Watermark propagation | Min of inputs | Min of inputs (same) |
| Late data | Configurable allowedLateness | "Grace period" (less specified) |
| Late data side output | Yes (explicit API) | Not documented |
| Count-based windows | Yes | Not documented |

**Largely aligned.** Wire's time model is a faithful mirror of Flink's. The main gap is the lack of a **side output** mechanism for late data — Flink allows routing late events to a separate stream for analysis.

---

## 7. Deployment

### Flink
- **3 deployment modes:** Application Cluster (dedicated per app), Session Cluster (shared long-running), Job Cluster (deprecated)
- **3 resource providers:** Standalone, Kubernetes, YARN
- External dependencies: ZooKeeper or K8s for HA, filesystem for checkpoints
- Client submits via CLI, REST API, SQL Client, Python REPL
- WebUI included

### Wire
- **2 deployment modes:** Standalone Cluster, Kubernetes Native
- Single binary deployment, zero external dependencies
- Configuration via `wire.yaml`
- No YARN support (appropriate — YARN is legacy)
- No CLI/REST/WebUI documented for job submission

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Deployment modes | Application, Session, (Job deprecated) | Standalone, Kubernetes |
| Resource providers | Standalone, K8s, YARN | Standalone, K8s |
| External deps | ZooKeeper/K8s HA + filesystem | None (single binary) |
| Job submission | CLI, REST, SQL Client, Python REPL | Not documented |
| WebUI | Yes | No |

**Wire's advantage:** Zero external dependencies is a massive operational win. Flink requires ZooKeeper (or K8s) for HA, a distributed filesystem for checkpoints, and a JVM. Wire is a single binary.

**Wire's gap:** No job submission API or interface documented. How do users actually submit and manage jobs?

---

## 8. Scaling & Rescaling

### Flink
- Savepoint → stop → change parallelism → restart from savepoint
- **Automatic state redistribution** using Key Groups
- Parallelism changes handled transparently via savepoint restoration
- Slot sharing means you only need slots = max parallelism

### Wire
- Same approach: Savepoint → stop → update config → restart from savepoint
- State rebalancing via Key Group reassignment (mirrors Flink)
- Explicitly states **no hot/dynamic scaling** (rescaling is stop-start)

**Aligned.** Wire's rescaling model is identical to Flink's. Both use Key Groups for state redistribution. Neither supports truly dynamic scaling.

---

## 9. API Abstraction Layers

### Flink
- **4 layers:** Stateful Stream Processing → DataStream API → Table API → SQL
- Users can mix layers (convert between DataStream and Table)
- SQL is the highest abstraction (declarative)
- Rich UDF support at every layer

### Wire
- **Not documented.** The technical doc mentions connectors and transforms but doesn't define a user-facing API hierarchy
- No SQL layer
- No Table API
- The only "API" documented is the `StateBackend` interface (internal)

**Wire's biggest doc gap.** Flink's multi-layer API is one of its defining features. Wire doesn't document how users actually write processing logic. Is there a Pipeline DSL? A YAML config? A Go SDK? This is fundamental and missing.

---

## 10. Networking & Data Transport

### Flink
- Uses **Netty** for inter-TaskManager data transport
- **Credit-based flow control** — downstream operators issue credits upstream
- Channels organized as InputGate → InputChannel per partition
- Network buffer pools with configurable size

### Wire
- Uses **HashiCorp Yamux** on port 4001
- **Window-based flow control** (Yamux built-in) + bounded Go channels
- Single persistent TCP connection per worker pair with multiplexed logical streams

### Key Differences

| Aspect | Flink | Wire |
|--------|-------|------|
| Transport | Netty (Java) | Yamux (Go) |
| Flow control | Credit-based (custom) | Window-based (Yamux built-in) |
| Connection model | Channel per partition | Multiplexed over single TCP connection |

**Different mechanisms, same goal.** Yamux is simpler and leverages an existing library. Flink's credit-based system is more fine-grained but more complex to implement and maintain.

---

## 11. Monitoring & Observability

### Flink
- Extensive metric system with reporters (Prometheus, JMX, Graphite, etc.)
- Hundreds of built-in metrics across: TaskManager, Job, Operator, Network, Checkpoint, IO, GC
- WebUI for live monitoring
- REST API for metric querying

### Wire
- Prometheus-compatible `/metrics` endpoint
- Key metrics documented: throughput, latency, buffer usage, checkpoint duration/alignment time
- Critical production alerts defined (checkpoint failure, restart loop, watermark stall)
- No WebUI, no REST API for metrics

**Wire's advantage:** Wire documents _what to alert on_ (production-ready operational guidance). Flink's docs list metrics but leave alerting strategy to the user.

**Wire's gap:** No WebUI or REST API for querying metrics. Relies entirely on external Prometheus + Grafana stack.

---

## 12. What Flink Has That Wire Doesn't Document

| Feature | Importance | Notes |
|---------|------------|-------|
| **User-facing API** (DataStream, Table, SQL) | Critical | Wire has no documented user API |
| **Job submission interface** (CLI, REST, WebUI) | Critical | How do users submit jobs? |
| **Unaligned checkpointing** | High | Important for backpressured pipelines |
| **Savepoint semantics** (UIDs, formats, claim modes) | High | Production-critical for upgrades |
| **Multiple state backends** (HashMap, RocksDB, ForSt) | Medium | Wire has only Pebble |
| **At-least-once mode** | Medium | Lower-latency option |
| **Slot sharing** | Medium | Resource optimization |
| **Concurrent snapshots** | Medium | Performance at scale |
| **Side outputs** (for late data, DLQ) | Medium | Error handling strategy |
| **Glossary** | Low | But useful for onboarding |
| **Batch execution mode** | Low | Wire is stream-focused |

---

## 13. What Wire Has That Flink Doesn't

| Feature | Notes |
|---------|-------|
| **Zero external dependencies** | Single binary, no ZooKeeper/YARN/JVM |
| **GPU design principles** | Opt-in GPU acceleration (Flink has no native GPU story) |
| **Per-task state isolation** | Dedicated Pebble per slot vs Flink's shared RocksDB |
| **Operational alerting guidance** | Documented what to alert on with thresholds |
| **Near-zero-cost snapshots** | Pebble hard-link checkpoints (milliseconds) |

---

## Overall Assessment

Wire's documentation is **architecturally faithful to Flink** in the areas it covers — the core streaming engine concepts (checkpointing, watermarks, state management, time semantics, rescaling) are well-documented and correctly mirror Flink's proven design.

The **biggest gaps** are not in the engine internals but in the **user-facing surface**:
1. **No API documentation** — How do users write Wire jobs?
2. **No job lifecycle** — How do users submit, monitor, cancel, upgrade jobs?
3. **No savepoint operations** — How do users do planned upgrades?

Wire's docs read like an **internal engineering spec** (how the engine works). Flink's docs serve both **engineers and users** (how to use it + how it works). Wire needs a "user layer" on top of its engine docs.
