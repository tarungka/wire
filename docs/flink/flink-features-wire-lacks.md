# Flink Features That Wire Does Not Have

**Status:** Reference
**Version:** 0.1.0
**Context:** Comprehensive gap analysis — everything Flink offers that Wire does not currently document or support

---

## Critical — Wire can't be used in production without these

### 1. User-Facing API / SDK

Flink has 4 abstraction layers: Process Function → DataStream API → Table API → SQL. Users build pipelines by chaining typed transformations (`map`, `filter`, `keyBy`, `window`, `reduce`, etc.) on a `DataStream` object, then call `execute()`. Programs follow lazy evaluation — transformations build a dataflow graph, actual execution happens only when triggered.

**Wire gap:** Zero documented user API. There is no way for a user to write a Wire job.

### 2. Job Submission & Lifecycle Management

Flink has CLI (`flink run`), REST API, SQL Client, Python REPL, and a built-in WebUI for submitting, monitoring, and canceling jobs. The Client is a separate component that compiles user code into a JobGraph before submission.

**Wire gap:** No submission interface, no REST API, no WebUI, no Client component documented.

### 3. Connector Ecosystem

Flink ships connectors for:
- **Message Queues:** Kafka, Pulsar, RabbitMQ, Google PubSub
- **Databases:** Cassandra, MongoDB, DynamoDB, JDBC
- **Cloud:** Kinesis Data Streams, Kinesis Firehose
- **Search:** Elasticsearch, OpenSearch
- **Other:** Prometheus (sink), FileSystem, DataGen (synthetic source), Hybrid Source
- **Community (Apache Bahir):** ActiveMQ, Flume, Redis, Netty

Plus a dedicated **Asynchronous I/O** API for enriching streams from external databases/APIs without blocking.

**Wire gap:** Connectors are mentioned in the technical doc but none are shipped or specified.

### 4. Savepoint Operations

Flink has a full savepoint system:
- **Operator UIDs:** Users assign stable IDs (`uid("source-id")`) so state maps correctly across code changes. Without these, any code modification can break state restoration.
- **Two formats:** Canonical (portable across backends, slower) and Native (fast, backend-specific e.g. RocksDB SST files)
- **Claim modes:** NO_CLAIM (user owns files), CLAIM (Flink manages lifecycle)
- **CLI commands:** Trigger, restore, dispose savepoints. `-n` flag to skip non-restored state for removed operators.
- **Relocatable:** Savepoints use relative paths and can be moved across storage locations.

**Wire gap:** "Savepoint" mentioned once in the context of rescaling. No operator UIDs, no formats, no lifecycle management.

---

## High Priority — production quality and operational maturity

### 5. Unaligned Checkpointing

Flink allows checkpoint barriers to overtake in-flight data. The overtaken records become part of the operator's checkpoint state. This means checkpoints complete quickly even when pipelines are heavily backpressured.

Trade-off: Higher I/O pressure and larger checkpoint size, but dramatically faster barrier propagation in slow pipelines.

**Wire gap:** Only supports aligned checkpoints. A slow downstream operator can block checkpoint completion for the entire pipeline — a significant production concern.

### 6. At-Least-Once Mode

Flink lets users skip barrier alignment for lower latency. Operators may process records from the next checkpoint epoch before the current snapshot completes. Recovery produces duplicates but latency stays consistently low.

Note: Embarrassingly parallel operations (`map`, `flatMap`, `filter`) provide exactly-once guarantees regardless of mode.

**Wire gap:** Exactly-once only. No way to trade consistency for latency when the use case allows it.

### 7. Side Outputs

Flink lets operators emit to multiple typed output streams beyond the main output:
- Define an `OutputTag<T>` with a name and type
- Emit via `ctx.output(tag, value)` from any ProcessFunction
- Retrieve via `mainStream.getSideOutput(tag)`

Use cases: late data routing, error/DLQ streams, stream splitting without duplication.

**Wire gap:** No side output mechanism. No way to route late data or errors to a separate stream.

### 8. High Availability

Flink has leader/standby JobManager failover:
- **ZooKeeper mode:** Leader election, metadata storage (pointers to checkpoints/job graphs)
- **Kubernetes mode:** Lease objects for leader election, ConfigMaps for metadata
- **JobResultStore:** Persists completed job results to filesystem for recovery

**Wire gap (addressed):** WIP-09 documents a Flink-inspired phased HA strategy: Phase A (PebbleDB metadata persistence), Phase B (pluggable leader election), Phase C (fencing tokens for split-brain prevention), Phase D (embedded Raft, deferred). Phases A-C provide the core HA mechanism; automatic failover depends on deployment infrastructure (systemd, Kubernetes) until Phase D is implemented.

### 9. Broadcast State Pattern

Flink can broadcast configuration/rules to all parallel operator instances:
- `BroadcastProcessFunction` for non-keyed streams
- `KeyedBroadcastProcessFunction` for keyed streams with timer support
- Read-write asymmetry: only broadcast side can modify state (ensures consistency)
- In-memory only (no RocksDB support)

Use cases: dynamic rule distribution, ML model updates, reference data enrichment.

**Wire gap:** Broadcast State listed as a concept in `state-backend.md` but no API or implementation details.

---

## Medium Priority — feature completeness

### 10. Slot Sharing

Flink lets subtasks from the same job share Task Slots. A cluster only needs slots = max parallelism across the job, not the sum of all operator parallelisms. This significantly reduces resource requirements.

**Wire gap:** Not documented. May require over-provisioning.

### 11. Concurrent Snapshots

Flink can have multiple checkpoint barriers in-flight simultaneously, enabling overlapping snapshot work. "Multiple barriers from different snapshots can coexist simultaneously."

**Wire gap:** Not mentioned. Unclear if Wire processes one checkpoint at a time.

### 12. Multiple State Backends

Flink offers 3 options:
- **HashMapStateBackend:** In-memory, fast, for small state or development
- **EmbeddedRocksDBStateBackend:** Disk-based, scalable, supports incremental checkpoints
- **ForStStateBackend:** Experimental, disaggregated to remote FS (HDFS/S3) for state exceeding local disk

**Wire gap:** Only Pebble. No in-memory option for simple/small-state jobs.

### 13. Additional State Types

Flink has `ReducingState` and `AggregatingState` beyond the basic types. These automatically apply a reduce/aggregate function on each state update, avoiding read-modify-write patterns.

**Wire gap:** Only ValueState, ListState, MapState.

### 14. State Changelog

Flink can continuously upload state changes (not just at checkpoint time). This reduces actual checkpoint duration since most data is already persisted. Trade-off: higher steady-state I/O and resource usage.

**Wire gap:** No equivalent.

### 15. Count-Based Windows

Flink supports windows that trigger on element count (e.g., "every 100 events"), not just time intervals.

**Wire gap:** Only time-based windows documented.

### 16. Asynchronous I/O API

Flink has a dedicated API for async calls to external systems during stream processing. This enables efficient enrichment from databases or web services without blocking the pipeline or wasting thread resources.

**Wire gap:** Not documented.

### 17. Client Component

Flink has a separate Client that compiles user applications into a JobGraph, applies optimizations, and submits to the cluster. The client can run in detached or attached mode.

**Wire gap:** No equivalent. Unclear how user logic gets compiled into an execution plan.

---

## Lower Priority — nice to have

### 18. WebUI

Flink includes a built-in web dashboard for monitoring jobs, viewing DAG visualizations, inspecting metrics, and managing cluster state.

**Wire gap:** Relies on external Prometheus + Grafana.

### 19. REST API for Metrics

Flink exposes all metrics via REST, enabling programmatic metric querying and integration with custom tooling.

**Wire gap:** Only a Prometheus `/metrics` endpoint.

### 20. Multi-Job Support

Flink runs multiple concurrent jobs with separate JobMaster instances per job. Jobs compete for shared TaskManager resources in session mode.

**Wire gap:** Not documented whether Wire supports concurrent jobs.

### 21. Application vs Session Cluster Modes

Flink supports dedicated per-app clusters (Application Mode — one cluster per application, better isolation) and shared session clusters (Session Mode — long-running, accepts multiple jobs).

**Wire gap:** Only standalone and K8s modes.

### 22. Batch Execution Mode

Flink can run bounded datasets without checkpointing overhead. Recovery relies on full stream replay rather than checkpoint restoration, which is more efficient for batch workloads.

**Wire gap:** Stream-only. No batch execution mode.

### 23. Glossary

Flink maintains a formal glossary (30+ terms) defining Event, Operator, Task, Sub-Task, Physical Graph, Logical Graph, Managed State, Operator Chain, Partition, Record, UID, etc.

**Wire gap:** No glossary.

### 24. Snapshot Compression

Flink supports optional Snappy compression for checkpoint data via `setUseSnapshotCompression(true)`.

**Wire gap:** No compression documented.

### 25. Checkpoint Retention Policies

Flink has configurable retention: `RETAIN_ON_CANCELLATION` (keep checkpoints after job cancel, requires manual cleanup) vs `DELETE_ON_CANCELLATION` (auto-delete on cancel, retain only for failures).

**Wire gap:** No checkpoint lifecycle management documented.

---

## Summary by Priority

| Priority | Count | Items |
|----------|-------|-------|
| **Critical** | 4 | User API, Job submission, Connectors, Savepoints |
| **High** | 5 | Unaligned checkpoints, At-least-once, Side outputs, ~~HA~~ (addressed — WIP-09), Broadcast state |
| **Medium** | 8 | Slot sharing, Concurrent snapshots, Multiple backends, Extra state types, Changelog, Count windows, Async I/O, Client |
| **Lower** | 8 | WebUI, REST metrics, Multi-job, Cluster modes, Batch mode, Glossary, Compression, Retention policies |

The first 4 critical items are the gap between "internal engine spec" and "usable software."
