# Technical Requirements Document (TRD)

> **Feature/Project:** `Goroutine & Concurrency Model`
>
> **WIP ID:** `WIP-16`
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

Wire's architecture.md states that "each Operator runs as a lightweight Goroutine chain within a slot" and that "Operator Chaining fuses sequential operators into a single Goroutine." But the **exact goroutine topology is never specified**: how many goroutines per Task Slot? Is the pool bounded or unbounded? How are async operations (checkpoint upload, Pebble compaction) managed? This affects memory consumption, scheduling behavior, and performance tuning.

### 1.2 Proposed Solution (Technical Summary)

Define the goroutine model per Task Slot: one main processing goroutine per operator chain, bounded input/output channel buffers, dedicated goroutines for async checkpoint upload and Yamux I/O, and a bounded worker pool for Pebble background operations.

---

## 2. Architecture & System Design

### 2.1 Goroutine Topology per Task Slot

```
┌─────────────────────────────────────────────────────────────┐
│                       Task Slot                              │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Operator Chain Goroutine (1 per chain)                │   │
│  │  Source.ReadBatch() → Map() → Filter() → output chan  │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────────┐   │
│  │ Yamux Read   │  │ Yamux Write  │  │ Checkpoint      │   │
│  │ Goroutine    │  │ Goroutine    │  │ Upload          │   │
│  │ (1 per input │  │ (1 per output│  │ Goroutine       │   │
│  │  stream)     │  │  stream)     │  │ (1, on-demand)  │   │
│  └──────────────┘  └──────────────┘  └─────────────────┘   │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐   │
│  │ Pebble Background Goroutines (bounded pool)           │   │
│  │ - Compaction (1-2)                                    │   │
│  │ - WAL sync (1)                                        │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Goroutine Inventory per Task Slot

| Goroutine | Count | Lifecycle | Purpose |
|-----------|-------|-----------|---------|
| **Operator Chain** | 1 per chain | Entire task lifetime | Execute fused operators sequentially |
| **Yamux Stream Reader** | 1 per input stream | Entire task lifetime | Read data from upstream tasks |
| **Yamux Stream Writer** | 1 per output stream | Entire task lifetime | Write data to downstream tasks |
| **Checkpoint Uploader** | 0-1 | Created on checkpoint, exits on completion | Upload Pebble checkpoint to S3 |
| **Watermark Emitter** | 1 per source task | Entire task lifetime (sources only) | Periodically emit watermark records |
| **Pebble Compaction** | 1-2 | Managed by Pebble | Background LSM compaction |
| **Pebble WAL Sync** | 1 | Managed by Pebble | Write-ahead log synchronization |

**Typical total per Task Slot:** 5-10 goroutines (varies with number of input/output streams).

### 2.3 Channel Buffer Sizes

| Channel | Buffer Size | Purpose |
|---------|------------|---------|
| Operator input (from Yamux) | 1024 events | Buffer between network read and operator processing |
| Operator output (to Yamux) | 1024 events | Buffer between operator output and network write |
| Checkpoint trigger | 1 | Signal from Coordinator to start checkpoint |
| Watermark | 1 | Latest watermark (overwrite semantics) |

### 2.4 Backpressure Propagation

```
Sink slow → output channel full → operator chain blocks on write →
input channel full → Yamux reader blocks on write →
Yamux window closes → upstream writer blocks →
... propagates to Source → Source stops fetching
```

All channels are **bounded**. No unbounded buffering anywhere in the pipeline. Backpressure is propagated via channel blocking + Yamux flow control.

---

## 3. API Design

### 3.1 Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `task_slot.input_buffer_size` | `1024` | Events buffered per input channel |
| `task_slot.output_buffer_size` | `1024` | Events buffered per output channel |
| `task_slot.checkpoint_upload_concurrency` | `1` | Max concurrent checkpoint uploads per task |
| `pebble.max_compaction_concurrency` | `2` | Max concurrent Pebble compaction goroutines |

### 3.2 GOMAXPROCS

Wire does **not** override `GOMAXPROCS`. It defaults to the number of available CPU cores. For containerized deployments, set `GOMAXPROCS` via `uber/automaxprocs` or explicitly via environment variable to match cgroup CPU limits.

---

## 4. Data Model & Storage

No persistent storage for goroutine model. All concurrency state is ephemeral.

---

## 5. Design Decisions & Trade-offs

### Decision 1: One goroutine per operator chain (not per operator)

|  |  |
| -- | -- |
| **Context** | Fused operators (Source → Map → Filter) could each have their own goroutine, or share one. |
| **Options Considered** | (A) One goroutine per chain, (B) One goroutine per operator with channels between them |
| **Decision** | Option A |
| **Rationale** | Eliminates channel overhead between chained operators. A `Map → Filter` chain becomes a single function call, not a channel send/receive pair. Matches Flink's operator chaining model. |
| **Trade-offs Accepted** | A slow operator in the chain blocks the entire chain. No per-operator parallelism within a chain. |
| **Revisit Trigger** | If specific operators become bottlenecks within chains. |

### Decision 2: Bounded channels (not unbounded)

|  |  |
| -- | -- |
| **Context** | Channel buffer sizing affects latency, throughput, and memory. |
| **Options Considered** | (A) Bounded channels (default 1024), (B) Unbounded channels (linked list), (C) Ring buffer with overwrite |
| **Decision** | Option A |
| **Rationale** | Bounded channels provide natural backpressure. Unbounded channels risk OOM. Ring buffer loses data. 1024 is large enough to absorb micro-bursts but small enough to limit memory (1024 events × ~1KB = ~1MB per channel). |
| **Trade-offs Accepted** | Under extreme load, producers block. Tail latency increases under backpressure. |
| **Revisit Trigger** | If 1024 proves too small for high-throughput pipelines. Make configurable. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Goroutine leak (e.g., Yamux reader not cleaned up on shutdown) | Context cancellation propagated to all goroutines. `defer` cleanup in each. | Resource leak if buggy | Medium |
| 2 | Pebble compaction goroutines compete with operator chain | Pebble compaction is bounded (max 2). CPU contention managed by Go scheduler. | Slight throughput reduction during compaction | Low |
| 3 | Checkpoint upload goroutine blocks on slow S3 | Upload is async — doesn't block the operator chain. If upload takes longer than checkpoint interval, next checkpoint may be delayed. | Checkpoint interval effectively increases | Medium |
| 4 | Channel deadlock (circular dependency) | Wire's DAG structure prevents circular data flow. Control channels (checkpoint, watermark) use non-blocking sends where possible. | Should not occur by design | Low |

---

## 7. Security & Compliance

No additional security considerations.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Channel buffer behavior, backpressure propagation | Go `testing` | Core data path |
| Benchmark Tests | Throughput per goroutine model, channel overhead | Go `testing.B` | Baseline established |
| Race Detection | All concurrent paths | `go test -race` | Zero race conditions |

### 8.1 Key Test Scenarios

1. Backpressure: Slow sink → verify source stops reading (not OOM)
2. Shutdown: Cancel context → all goroutines exit within 5 seconds
3. Checkpoint: Async upload doesn't block operator chain processing
4. Race: Concurrent ReadBatch + GenerateWatermark on source (thread safety)

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should channel buffer sizes be auto-tuned based on throughput? | Tarun | Open |
| 2 | Should we use `uber/automaxprocs` for container-aware GOMAXPROCS? | Tarun | Open |
| 3 | Risk: Too many Task Slots per worker = too many goroutines = Go scheduler overhead | — | Acknowledged |
