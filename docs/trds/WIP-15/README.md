# Technical Requirements Document (TRD)

> **Feature/Project:** `Glossary of Terms`
>
> **WIP ID:** `WIP-15`
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

Wire's documentation uses specialized terms (Key Group, Epoch, Barrier, Task Slot, Watermark, Savepoint, etc.) that are **never formally defined in one place**. New contributors and users must piece together definitions from scattered context across 5+ documents, leading to misunderstanding and inconsistent usage.

### 1.2 Proposed Solution (Technical Summary)

Create a single authoritative glossary that defines every Wire-specific term with a concise definition, the document where it's formally specified, and cross-references to related terms.

---

## 2. Glossary

| Term | Definition | Defined In |
|------|-----------|------------|
| **Barrier** | A control record injected by the Coordinator into source streams to delimit checkpoint epochs. Barriers flow with data and trigger state snapshots when they arrive at operators. Also called "Checkpoint Barrier." | execution-model.md §5.1 |
| **Barrier Alignment** | The process by which a multi-input operator waits for Barrier N on ALL inputs before snapshotting. Input channels that receive the barrier early are blocked; their post-barrier data is buffered until alignment completes. | execution-model.md §5.2, WIP-10 |
| **Broadcast State** | A special state type sent to all parallel instances of an operator. Used for configuration data that every subtask needs (e.g., feature flags, rule sets). | state-backend.md §1.1 |
| **Checkpoint** | A consistent global snapshot of all operator state and source offsets. Triggered periodically by the Coordinator. Used for failure recovery. Checkpoints may be garbage-collected by the system. | execution-model.md §5.3 |
| **Checkpoint Barrier** | See **Barrier**. |  |
| **Coordinator** | The control plane node responsible for job management, checkpoint coordination, resource management, and failure recovery. In HA mode, a Raft leader among coordinator replicas. | architecture.md §1.1, WIP-07 |
| **DataStream** | The SDK abstraction representing an unbounded sequence of events flowing through a pipeline. Supports transformation operators (Map, Filter, KeyBy, etc.). | WIP-01 §3.2 |
| **Dead Letter Queue (DLQ)** | A side output sink where events that fail processing (poison messages) are routed instead of crashing the job. | WIP-13 |
| **Epoch** | The logical time period between two consecutive Checkpoint Barriers. Epoch N contains all events processed between Barrier N-1 and Barrier N. | execution-model.md §5.1 |
| **Event** | The atomic unit of data in Wire. Immutable, timestamped, optionally keyed. Consists of Key (bytes), Value (bytes), EventTime (int64), and Headers (map). | execution-model.md §1 |
| **Event Time** | The timestamp embedded in the event data, representing when the event actually occurred in the real world. Used for windowing and watermarks. Contrasted with Processing Time. | vision.md §4, execution-model.md §2.1 |
| **Exactly-Once Semantics (EOS)** | The guarantee that the effect of processing a record on system state and output is reflected exactly once, even in the presence of failures. Requires replayable sources and transactional/idempotent sinks. | vision.md §3.1 |
| **ExecutionGraph** | The physical parallel plan. Maps each logical operator to N parallel task instances distributed across workers. Created from the JobGraph by applying parallelism. | architecture.md §3.3 |
| **JobGraph** | The optimized logical plan. Created from the StreamGraph by operator chaining and shuffle/forward edge insertion. | architecture.md §3.2 |
| **Key Group** | The atomic unit of state redistribution. Keys are hashed to Key Groups (default 128). Key Groups are range-assigned to parallel task instances. Enables rescaling without per-key state migration. | state-backend.md §3.3, WIP-12 |
| **KeyedStream** | A DataStream partitioned by key via the `KeyBy()` operator. Required for stateful processing and windowing. | WIP-01 §3.3 |
| **Offset** | An opaque, serializable position in a source stream. Examples: Kafka partition offset, SQS receipt handle, file byte position. Stored in checkpoints for replay on recovery. | WIP-02 §3.1 |
| **Operator** | A processing unit in the dataflow graph. Examples: Map, Filter, KeyBy, Window, Process, Source, Sink. | architecture.md §2.1 |
| **Operator Chaining** | An optimization where sequential compatible operators (e.g., Source → Map → Filter) are fused into a single goroutine to avoid serialization overhead. | architecture.md §2.1 |
| **Pebble** | The default embedded key-value store (by CockroachDB) used as Wire's state backend. LSM-tree based, Go-native, supports instant hard-link checkpoints. | state-backend.md §3 |
| **Processing Time** | The wall-clock time of the machine processing the event. Used only for timeouts and metrics, never for correctness. | execution-model.md §2.1 |
| **Savepoint** | A user-triggered, named checkpoint that persists until explicitly deleted. Used for planned operations: upgrades, rescaling, debugging. Unlike automatic checkpoints, savepoints are not garbage-collected. | WIP-04 §3.8 |
| **Side Output** | A secondary output channel from an operator. Used for DLQ routing, late data, and routing events to multiple downstream paths. | WIP-01 §2.6 |
| **Sink** | A connector that writes processed events to an external system (Kafka, Postgres, S3, etc.). | WIP-02 §3.2 |
| **Source** | A connector that reads events from an external system into Wire. Must be replayable for exactly-once guarantees. | WIP-02 §3.1 |
| **StreamGraph** | The logical DAG defined by user code or YAML. Nodes are logical operators, edges are logical data streams. The first representation of a pipeline before optimization. | architecture.md §3.1 |
| **Task Slot** | A fixed allocation of resources (CPU, RAM) on a worker that can run one parallel instance of an operator. The total number of Task Slots across the cluster determines max parallelism. | architecture.md §2.1 |
| **TransactionalSink** | A Sink that supports two-phase commit for exactly-once delivery. Implements BeginTransaction, PreCommit, Commit, Abort. | WIP-02 §3.3, WIP-08 |
| **Two-Phase Commit (2PC)** | The protocol used to achieve exactly-once delivery to transactional sinks. Phase 1 (PreCommit) happens at barrier arrival; Phase 2 (Commit) happens at global checkpoint completion. | WIP-08 |
| **Watermark** | A control record declaring "no more events with timestamp < T will arrive." Flows through the graph, triggers window closures and timer firings. Generated by sources, propagated via Min() across inputs. | execution-model.md §2.2, WIP-11 |
| **Window** | A mechanism that groups events into finite temporal buckets for aggregation. Types: Tumbling (fixed, non-overlapping), Sliding (fixed, overlapping), Session (gap-based). | execution-model.md §3 |
| **Worker** | A data plane node that executes Task Slots. Hosts Pebble state, manages TCP connections for data shuffle, reports to Coordinator via heartbeat. | architecture.md §1.2 |
| **Yamux** | HashiCorp's multiplexing library used by Wire for efficient TCP connection sharing between nodes. A single TCP connection carries multiple logical streams (one per task-to-task channel). | architecture.md §2.2 |

---

## 3. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should the glossary be a standalone page in the docs root (not a TRD)? | Tarun | Open |
| 2 | Should terms link directly to their source documents (hyperlinks)? | Tarun | Open |
