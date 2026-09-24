# Execution Model

**Status:** Canon
**Version:** 1.0.0
**Context:** The Physics of the Engine

---

## 1. Event Model

In Wire, the atomic unit of processing is the **Event**.

An Event is:
*   **Mutable Go value:** `sdk.Event` aliases the engine event struct. Operators can produce modified values; do not mutate payloads concurrently or after handing them downstream.
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
2.  **Processing Time:** The wall clock of the worker. Used for runtime timeouts, metrics, and the optional ingestion-time timestamp strategy.

### 2.2 Watermarks
A **Watermark(T)** is a control packet flowing through the stream that declares:
> "No more events with timestamp < T will arrive."

*   **Generation:** Runtime watermark strategies observe event timestamps and emit ordered periodic boundaries. The SDK defaults to bounded out-of-orderness with a five-second tolerance; `SetWatermarkStrategy` selects another strategy. The source interface retains `GenerateWatermark` for compatibility, but execution does not call it. See [WIP-04](trds/WIP-04/README.md).
*   **Propagation:**
    *   Operators forward the *minimum* watermark received from all upstream inputs.
    *   `OutputWatermark = Min(InputWatermark_1, InputWatermark_2, ...)`
*   **Function:** Watermarks trigger **Window Calculations** and expire timers.

### 2.3 Late Data
An event with `Timestamp < CurrentWatermark` is late. Window eligibility is
separate: a window accepts it while `CurrentWatermark < WindowEnd + AllowedLateness`.
Zero lateness purges at window end, but an overlapping window that is still open
can accept the record. If every assigned window has expired, the original record
is sent once to the configured named late stream or dropped with a metric.

Configure retention per window with `AllowedLateness(30 * time.Second)` in the
SDK or `allowed_lateness: "30s"` in YAML. Retained windows emit updated results
with bounds and `IsUpdate=true`; prior results are not retracted. Named late
streams are separate from error-policy DLQs. See [WIP-12's runtime contract](trds/WIP-12/runtime-contract.md)
for side-output configuration, metrics, persistence and recovery.

---

## 3. Windowing Model

Windowing assigns events to finite temporal buckets.

### 3.1 Supported Windows
1.  **Tumbling Windows:** Fixed size, non-overlapping (e.g., "Every 5 minutes").
2.  **Sliding Windows:** Fixed size, overlapping (e.g., "Every 1 minute, look back 5 minutes").
3.  **Session Windows:** Dynamic size, gap-based (e.g., "User activity until 30m idle").

### 3.2 State Scope
*   **Window State:** State is scoped to `(Key, WindowID)`.
*   **Cleanup:** Retained window accumulators are removed when their lateness deadline is reached. The current window processor maintains accumulators in memory and supports snapshots; this is not a direct Pebble range-delete operation. YAML window graph parsing does not imply YAML window execution support. See [WIP-12](trds/WIP-12/README.md) and [YAML limits](../sdk/pipeline_yaml.md).

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
2.  **Local Snapshot:** The aligned operator chain captures immutable state through snapshot hooks. Pebble-backed state creates a consistent local checkpoint and manifest; other backends/operators serialize their state.
3.  **Persist:** Background workers replicate the checkpoint data to the durable store (checkpoint artifact replicas on peer workers).
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
*   **Internal State:** Checkpoint-managed state can be restored to a consistent completed snapshot. Application state outside checkpoint hooks is not covered, and embedded execution does not provide the distributed durable checkpoint service.
*   **Sink Output:**
    *   **Idempotent Sinks (KV Store):** Naturally Exactly-Once.
    *   **Transactional Sinks:** Use checkpoint-driven two-phase commit. Preparation precedes snapshot capture and replication; a durable global decision authorizes idempotent commit. Recovery fences prior writers, resolves orphan transactions and finishes the selected commit before replay. Aborts require task recovery, and bounded sources coordinate a final checkpoint. See the [WIP-10 runtime contract](trds/WIP-10/runtime-contract.md) for the public SDK interface, connector obligations and runtime limits.
    *   **Standard Sinks:** At-Least-Once (may see duplicates after replay).


## 7. Operator Errors and Dead Letter Queues

The default operator error policy fails the task. Per-operator policies can
retry transient failures with bounded backoff, drop a failed record, or send its
original payload and error metadata to a configured DLQ sink. Poison errors and
processing panics skip retries; fatal resource/state errors fail immediately.
A retry blocks progress of that operator chain, including checkpoint barriers,
until the call succeeds, exhausts its policy or is cancelled. User calls must
honor cancellation; a backoff cap cannot bound an uncooperative user function.

Only successful attempts publish normal output. Retryable state mutations and
external side effects must tolerate repeated execution. DLQ writes are
synchronous, cancellable and best effort: missing/failed destinations log and
count drops, and replay can duplicate DLQ records. DLQ delivery does not
participate in checkpoint transactions. See [error-policy usage](sdk/error_handling.md)
and [WIP-11 acceptance](trds/WIP-11/acceptance.md).
