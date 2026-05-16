# Execution Model

**Status:** Canon
**Version:** 1.0.0
**Context:** The Physics of the Engine

---

> **Current implementation boundary:** This document describes the target
> execution model. Engine primitives for watermarks, barriers, checkpoint
> metadata, and state exist, but the current cluster path does not yet provide
> end-to-end keyed shuffle, checkpoint/restore, exactly-once semantics, or
> distributed keyed windows. See [current-capabilities.md](current-capabilities.md).

## 1. Event Model

In Wire, the atomic unit of processing is the **Event**.

An Event is:
*   **Immutable:** Once created, it cannot be changed.
*   **Timestamped:** Every event carries an explicit `EventTime` (int64).
*   **Keyed (Optional):** Events may have a Partition Key which determines their routing.

### 1.1 Stream Properties
*   **Ordered:** Events within a specific **Key Range** are guaranteed to be processed in order.
*   **Bounded or Unbounded:** The engine treats all data as potentially infinite. Bounded data (files) is just a special case of a stream that closes.

---

## 2. Time Semantics & Watermarks

Wire strictly separates the "When it happened" from "When we saw it".

### 2.1 The Two Clocks
1.  **Event Time:** The timestamp embedded in the record (e.g., `click_time`). This determines results.
2.  **Processing Time:** The wall clock of the worker. Used *only* for timeouts or metrics, never for correctness.

### 2.2 Watermarks
A **Watermark(T)** is a control packet flowing through the stream that declares:
> "No more events with timestamp < T will arrive."

*   **Generation:** Source Connectors generate watermarks based on observed data (monotonically increasing).
*   **Propagation:**
    *   Operators forward the *minimum* watermark received from all upstream inputs.
    *   `OutputWatermark = Min(InputWatermark_1, InputWatermark_2, ...)`
*   **Function:** Watermarks trigger **Window Calculations** and expire timers.

### 2.3 Late Data
If an event arrives with `Timestamp < CurrentWatermark`:
*   **Default:** The event is dropped (or sent to a side-output "Dead Letter Queue").
*   **Allowed Lateness:** Users can configure a grace period where late events trigger a window re-computation/update.

---

## 3. Windowing Model

Windowing assigns events to finite temporal buckets.

### 3.1 Supported Windows
1.  **Tumbling Windows:** Fixed size, non-overlapping (e.g., "Every 5 minutes").
2.  **Sliding Windows:** Fixed size, overlapping (e.g., "Every 1 minute, look back 5 minutes").
3.  **Session Windows:** Dynamic size, gap-based (e.g., "User activity until 30m idle").

### 3.2 State Scope
*   **Window State:** State is scoped to `(Key, WindowID)`.
*   **Cleanup:** When `Watermark > Window_End + Lateness`, the window state is automatically purged from Pebble.

---

## 4. Backpressure & Flow Control

Wire utilizes a combination of **Yamux Flow Control** and Go Channels to manage load.

1.  **Yamux Streams:** Inter-node communication happens over Yamux streams. If the receiver is slow, the Yamux window closes, pausing the sender.
2.  **Bounded Channels:** Intra-node communication uses bounded Go channels.
3.  **Propagation:** If a Sink is slow:
    *   The Sink's input buffer fills.
    *   The upstream Operator blocks on write (channel full).
    *   Yamux stops reading from the network for that stream.
    *   ... This propagates recursively to the Source.
4.  **Source Behavior:** When blocked, the Source stops reading from external systems (e.g., stops accepting events from external systems).

**Guarantee:** No unlimited buffering. No silent data drops under load.

---

## 5. Checkpointing (The Core Algorithm)

Wire guarantees consistency using the **Asynchronous Barrier Snapshot (ABS)** algorithm (a variant of Chandy-Lamport).

### 5.1 The Barrier
A **Checkpoint Barrier (ID=N)** is a control record injected by the Coordinator into all Source streams.
*   It flows strictly linearly with data.
*   It divides the stream into "Epoch N" (pre-barrier) and "Epoch N+1" (post-barrier).

### 5.2 Barrier Alignment (Critical)
For operators with multiple inputs (e.g., `CoProcess`, `Join`, `KeyBy`):
1.  **Wait:** When Barrier N arrives on Input A, the operator stops processing Input A.
2.  **Buffer:** Records arriving on Input A (belonging to Epoch N+1) are buffered.
3.  **Process:** The operator continues processing Input B until Barrier N arrives on Input B.
4.  **Snapshot:** Once ALL inputs have received Barrier N, the operator triggers a state snapshot.
5.  **Forward:** The operator emits Barrier N downstream and unblocks Input A.

This alignment ensures the snapshot captures **exactly** the state of "All events <= Barrier N".

### 5.3 Snapshot Lifecycle
1.  **Trigger:** Coordinator sends `TriggerCheckpoint(N)` to Sources.
2.  **Local Snapshot:** Each operator creates an async **Pebble Checkpoint** (hard link) of its local state.
3.  **Persist:** Background workers replicate the checkpoint data to the durable store (replicated PebbleDB on peer nodes).
4.  **Acknowledge:** Workers notify the Coordinator.
5.  **Complete:** When all tasks ACK Checkpoint N, it is marked "Global Complete".

---

## 6. Failure & Recovery

### 6.1 Failure Detection
*   Coordinator monitors heartbeats from Workers.
*   If a worker is lost, the Job is marked `FAILING`.

### 6.2 Recovery Procedure
1.  **Cancel:** All running tasks are cancelled.
2.  **Restore:** The Coordinator selects the latest **Completed Checkpoint (N)**.
3.  **Reschedule:** Tasks are redeployed.
4.  **State Load:** Tasks download their specific state shard for Checkpoint N from storage into Pebble.
5.  **Rewind:** Sources reset their read offsets to those recorded in Checkpoint N.
6.  **Resume:** Processing restarts from Epoch N+1.

### 6.3 Exactly-Once vs At-Least-Once
*   **Internal State:** Always Exactly-Once (due to rollback).
*   **Sink Output:**
    *   **Idempotent Sinks (KV Store):** Naturally Exactly-Once.
    *   **Transactional Sinks:** Require "Two-Phase Commit" tied to the Checkpoint completion mechanism.
    *   **Standard Sinks:** At-Least-Once (may see duplicates after replay).
