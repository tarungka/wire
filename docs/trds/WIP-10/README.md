# Technical Requirements Document (TRD)

> **Feature/Project:** `Barrier Alignment Timeout & Failure Handling`
>
> **WIP ID:** `WIP-10`
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

Wire's execution-model.md describes the barrier alignment algorithm (Chandy-Lamport ABS) for the happy path — barriers arrive on all inputs, the operator snapshots, and the barrier is forwarded. But it **never specifies what happens when a barrier fails to arrive on one or more inputs**. Does the checkpoint timeout? Is the entire job killed? Is the stalled input skipped? This is critical for correctness — a missing barrier can stall the entire pipeline indefinitely.

### 1.2 Proposed Solution (Technical Summary)

Implement a configurable checkpoint timeout at the Coordinator level. If a checkpoint does not complete (all ACKs received) within the timeout, the checkpoint is aborted. Operators that have partially aligned (received barriers on some inputs) release their buffered data and discard the incomplete checkpoint. Optionally, consecutive checkpoint timeouts trigger job failure to prevent indefinite degradation.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define checkpoint timeout behavior | Unaligned checkpoints (Flink-style) |
| Specify operator behavior during alignment stall | Automatic root-cause analysis of stalls |
| Define consecutive failure thresholds | Self-healing barrier injection |
| Specify metrics for monitoring alignment health | Partial checkpoint completion |

---

## 2. Architecture & System Design

### 2.1 Timeout Flow

```
Coordinator                      Operator (2 inputs)
    │                               │
    │  TriggerCheckpoint(N)         │
    ├──────────────────────────────▶│
    │                               │
    │                    Barrier N arrives on Input A
    │                    ┌──────────┤
    │                    │ Buffer   │ ← Input A blocked, Input B still processing
    │                    │ Input A  │
    │                    └──────────┤
    │                               │
    │  [TIMEOUT expires]            │ ← Barrier N never arrives on Input B
    │                               │
    │  AbortCheckpoint(N)           │
    ├──────────────────────────────▶│
    │                               │
    │                    ┌──────────┤
    │                    │ Release  │ ← Buffered Input A data released
    │                    │ buffers  │ ← Alignment discarded
    │                    │ Resume   │ ← Normal processing resumes
    │                    └──────────┤
    │                               │
    │  TriggerCheckpoint(N+1)       │ ← Next attempt
    ├──────────────────────────────▶│
```

### 2.2 Component Breakdown

**Component 1:** Checkpoint Coordinator Timer
* **Responsibility:** Track per-checkpoint timeout. If not all ACKs received within timeout, abort the checkpoint.
* **Technology:** Go `time.Timer` in Coordinator
* **Interactions:** Started on `TriggerCheckpoint(N)`. Canceled on completion. Fires `AbortCheckpoint(N)` on expiry.

**Component 2:** Operator Alignment State
* **Responsibility:** Track which inputs have received Barrier N. Buffer post-barrier data on aligned inputs. Release buffers on abort.
* **Technology:** Per-operator state in Task Slot runtime
* **Interactions:** Receives barriers from upstream. Receives AbortCheckpoint from Coordinator.

### 2.3 Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `checkpoint.timeout` | `10m` | Max time for a checkpoint to complete |
| `checkpoint.min_pause` | `0s` | Minimum gap between checkpoint completions |
| `checkpoint.max_consecutive_failures` | `0` | Consecutive timeouts before job failure (0 = unlimited) |
| `checkpoint.tolerable_failure_rate` | `0` | Fraction of checkpoints allowed to fail (0 = no tolerance) |

---

## 3. API Design

### 3.1 AbortCheckpoint RPC

```
AbortCheckpoint(checkpoint_id: int64) → Ack
```

Sent from Coordinator to all tasks when a checkpoint times out. Tasks must:
1. Release any buffered alignment data.
2. Discard partial snapshot data (if snapshot was started).
3. Resume normal processing.
4. Not forward the barrier downstream.

### 3.2 Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `wire_checkpoint_timeout_total` | Counter | Number of checkpoints that timed out |
| `wire_checkpoint_alignment_time_ms` | Histogram | Time spent waiting for barrier alignment per operator |
| `wire_checkpoint_alignment_buffered_bytes` | Gauge | Bytes buffered during alignment per operator |

---

## 4. Data Model & Storage

No new persistent storage. Alignment state is ephemeral (in-memory buffers).

---

## 5. Design Decisions & Trade-offs

### Decision 1: Abort-and-retry (not skip-and-continue)

|  |  |
| -- | -- |
| **Context** | When a checkpoint times out, we could skip it or abort it. |
| **Options Considered** | (A) Abort checkpoint, release buffers, try next one; (B) Skip the stalled input and complete partial checkpoint; (C) Kill the job |
| **Decision** | Option A: Abort and retry |
| **Rationale** | Partial checkpoints violate the exactly-once guarantee (state is inconsistent). Killing the job is too aggressive. Abort + retry gives the system a chance to recover (e.g., if a slow node catches up). |
| **Trade-offs Accepted** | One checkpoint interval of progress is lost. Repeated timeouts waste resources. |
| **Revisit Trigger** | If unaligned checkpoints are implemented (like Flink's unaligned mode). |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | One input permanently stalled (dead upstream) | Consecutive timeouts exceed threshold → job enters FAILING | Job restart | High |
| 2 | Network partition delays barrier delivery | Timeout fires. On reconnect, delayed barrier is ignored (wrong epoch). Next checkpoint succeeds. | One lost checkpoint | Medium |
| 3 | Operator has 10 inputs, 9 aligned, 1 missing | All 9 inputs' buffered data released on timeout. Significant memory pressure during alignment. | Memory spike | Medium |
| 4 | Checkpoint timeout set too low (< alignment time) | Every checkpoint times out. No progress. | Wasted resources | High |
| 5 | AbortCheckpoint arrives after operator already completed snapshot | Operator ignores abort (already forwarded barrier). Coordinator still counts as timed out. | No data impact | Low |

---

## 7. Security & Compliance

No additional security considerations beyond existing RPC authentication (WIP-09).

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Coordinator timeout logic, operator alignment state machine | Go `testing` | 100% of timeout/abort paths |
| Integration Tests | Stall one input, verify timeout + recovery | MiniCluster + toxiproxy | Happy path + timeout + consecutive failure |

### 8.1 Key Test Scenarios

1. Normal: All barriers arrive within timeout → checkpoint completes
2. One input stalled: Barrier missing → timeout → abort → next checkpoint succeeds
3. Consecutive failures: 3 timeouts in a row → job enters FAILING (if threshold = 3)
4. Buffer release: Verify no data loss after alignment abort (buffered events replayed)

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should we implement unaligned checkpoints (Flink-style) to avoid alignment blocking? | Tarun | Open |
| 2 | What is a reasonable default for max_consecutive_failures? | Tarun | Open |
| 3 | Should alignment buffer size be bounded (spill to disk if too large)? | Tarun | Open |
