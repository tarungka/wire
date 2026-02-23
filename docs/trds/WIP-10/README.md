# Two-Phase Commit for Transactional Sinks

> **Feature/Project:** `Two-Phase Commit for Transactional Sinks`
>
> **WIP ID:** `WIP-10`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-23`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |
| 0.2 | 2026-02-23 | Tarun Ashok | Removed connector-specific mappings; scoped to protocol only |

---

## 1. Overview

### 1.1 Problem Statement

Wire's execution-model.md states that exactly-once semantics for external sinks require "transactional or idempotent" sinks, but **never defines the two-phase commit (2PC) protocol**, the pre-commit/commit hooks, or how sink transactions integrate with the checkpoint lifecycle. Without this specification, it is impossible to implement exactly-once delivery to any external system.

### 1.2 Proposed Solution (Technical Summary)

Define a two-phase commit protocol where the checkpoint lifecycle drives the transaction lifecycle. Phase 1 (PreCommit) occurs when a checkpoint barrier arrives at a sink — the sink flushes all buffered data and prepares its transaction. Phase 2 (Commit) occurs when the Coordinator confirms global checkpoint completion — the sink finalizes the transaction. On failure, Abort rolls back any in-flight transaction and processing resumes from the last committed checkpoint.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define the 2PC protocol tied to checkpoint barriers | Distributed transactions across multiple sinks |
| Specify PreCommit, Commit, Abort semantics | Exactly-once for non-transactional sinks |
| Define failure recovery for each phase | Cross-system atomic commits |
| Document how custom connectors implement 2PC | Saga pattern or compensating transactions |
| Specify idempotent Commit requirements | Read-your-own-writes consistency |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| 2PC protocol specified | No specification | Complete protocol with sequence diagram | Doc review |
| TransactionalSink implementable from spec | No | Yes | Implementation test |

---

## 2. Architecture & System Design

### 2.1 High-Level Architecture

```
Coordinator                     Worker (Sink Task)               External System
    │                               │                               │
    │ TriggerCheckpoint(N)          │                               │
    ├──────────────────────────────▶│                               │
    │                               │                               │
    │                    ┌──────────┤                               │
    │                    │ Barrier N│arrives                        │
    │                    │ at sink  │                               │
    │                    └──────────┤                               │
    │                               │                               │
    │                               │──PreCommit(N)────────────────▶│
    │                               │  (flush + prepare tx)        │
    │                               │◀────────────── prepared ──────│
    │                               │                               │
    │  AcknowledgeCheckpoint(N)     │                               │
    │◀──────────────────────────────│                               │
    │                               │                               │
    │  ... wait for ALL tasks ...   │                               │
    │                               │                               │
    │  Checkpoint N COMPLETE        │                               │
    │──────────────────────────────▶│                               │
    │                               │                               │
    │                               │──Commit(N)───────────────────▶│
    │                               │  (finalize tx)               │
    │                               │◀────────────── committed ─────│
    │                               │                               │
    │                               │──BeginTransaction()──────────▶│
    │                               │  (start next tx)             │
    │                               │                               │
```

### 2.2 Component Breakdown

**Component 1:** Checkpoint Coordinator
* **Responsibility:** Triggers checkpoints, collects ACKs, declares global completion.
* **Technology:** Coordinator RPC (see WIP-07)
* **Interactions:** Sends TriggerCheckpoint to sources, receives AcknowledgeCheckpoint from all tasks, then broadcasts Commit notification.

**Component 2:** Sink Task Runtime
* **Responsibility:** Manages the TransactionalSink lifecycle within the checkpoint protocol.
* **Technology:** Go runtime wrapping the TransactionalSink interface (see WIP-16)
* **Interactions:** Calls PreCommit on barrier arrival, Commit on global completion notification, Abort on failure.

**Component 3:** TransactionalSink Implementation
* **Responsibility:** Maps Wire's 2PC phases to the external system's transaction semantics.
* **Technology:** Connector-specific implementation of the `TransactionalSink` interface
* **Interactions:** Translates PreCommit/Commit/Abort to external system calls.

### 2.3 Data Flow — Full Checkpoint-Transaction Cycle

1. **Coordinator** triggers Checkpoint N by injecting barriers into all source streams.
2. Barriers flow through the operator graph (with alignment per execution-model.md).
3. Each **operator** snapshots its Pebble state when the barrier arrives.
4. **Sink** receives the barrier:
   a. Calls `sink.PreCommit(ctx, N)` — flushes all buffered writes, prepares the transaction.
   b. Sends `AcknowledgeCheckpoint(N, taskID, stateHandle)` to Coordinator.
5. **Coordinator** collects ACKs from ALL tasks.
6. When all ACKs received: Checkpoint N is globally complete.
7. Coordinator notifies all sink tasks: **Commit Checkpoint N**.
8. **Sink** calls `sink.Commit(ctx, N)` — finalizes the transaction in the external system.
9. **Sink** calls `sink.BeginTransaction(ctx)` — starts the next transaction for Epoch N+1.

### 2.4 Failure Recovery

**Failure during Phase 1 (before global completion):**
1. Job enters FAILING state.
2. All tasks are canceled.
3. Sink tasks call `sink.Abort(ctx)` — rolls back any prepared-but-not-committed transactions.
4. Job restarts from last **committed** Checkpoint (N-1).
5. Sink re-opens and calls `BeginTransaction(ctx)` — fresh transaction.
6. Events from Epoch N are reprocessed. No duplicates because the Epoch N transaction was aborted.

**Failure during Phase 2 (Commit):**
1. If `Commit(N)` succeeds on some sinks but the ACK is lost, on restart the Coordinator re-sends the Commit notification.
2. Sink implementations **must handle idempotent Commit** — committing the same checkpointID twice must be a no-op.
3. If `Commit(N)` fails (e.g., external system down), the sink retries with exponential backoff.
4. If retries are exhausted, the job enters FAILING. On restart, the Coordinator will re-attempt the Commit.

---

## 3. API Design

### 3.1 TransactionalSink Interface (from WIP-16)

```go
type TransactionalSink interface {
    Sink

    // BeginTransaction starts a new transaction context.
    // Called once at startup and after each Commit.
    BeginTransaction(ctx context.Context) error

    // PreCommit flushes all buffered data and prepares the transaction.
    // After PreCommit returns, no more WriteBatch calls occur until Commit or Abort.
    // This is Phase 1 of the two-phase commit.
    PreCommit(ctx context.Context, checkpointID int64) error

    // Commit finalizes the transaction. Called ONLY after global checkpoint completion.
    // Must be idempotent — calling Commit(N) twice must be safe.
    // This is Phase 2 of the two-phase commit.
    Commit(ctx context.Context, checkpointID int64) error

    // Abort rolls back the current transaction.
    // Called on failure recovery before restarting from a checkpoint.
    Abort(ctx context.Context) error
}
```

### 3.2 Implementing 2PC for Custom Connectors

Custom connectors that support transactions must map Wire's 2PC phases to the external system's transaction primitives:

| Wire 2PC Phase | External System Equivalent |
|----------------|---------------------------|
| `BeginTransaction()` | Open a transaction / start a write session |
| `WriteBatch(events)` | Write data within the transaction boundary |
| `PreCommit(N)` | Flush all buffered data, ensure transaction is durable but not yet visible |
| `Commit(N)` | Make the transaction visible / finalize it |
| `Abort()` | Roll back all uncommitted data |

**Requirements for implementors:**
- `Commit(N)` must be **idempotent** — calling it twice with the same checkpointID must be safe. This can be achieved by tracking committed checkpoint IDs in a metadata table/store.
- `PreCommit(N)` must guarantee that all data written via `WriteBatch` is durable (flushed to the external system, not just buffered locally).
- `Abort()` must cleanly roll back without side effects. After Abort, the sink will be closed and re-opened for recovery.
- Transaction duration equals the checkpoint interval. Implementors should ensure the external system can hold transactions open for that duration.

---

## 4. Data Model & Storage

### 4.1 Checkpoint-Transaction State

The Coordinator tracks per-sink-task transaction state:

| Field | Type | Description |
| -- | -- | -- |
| task_id | string | Sink task identifier |
| current_checkpoint | int64 | Checkpoint currently in PreCommit |
| last_committed_checkpoint | int64 | Last successfully committed checkpoint |
| transaction_state | enum | ACTIVE / PRE_COMMITTED / COMMITTED |

### 4.2 Recovery Metadata

On recovery, the Coordinator determines which transactions need Commit vs Abort:

- If `checkpoint N` is globally complete but a sink has `last_committed_checkpoint = N-1` → re-send Commit(N).
- If `checkpoint N` is NOT complete → Abort any in-flight transactions and restart from last committed checkpoint.

---

## 5. Design Decisions & Trade-offs

### Decision 1: Checkpoint-driven 2PC (not independent transaction boundaries)

|  |  |
| -- | -- |
| **Context** | Transaction boundaries must align with checkpoints for exactly-once. |
| **Options Considered** | (A) 2PC tied to checkpoint lifecycle, (B) Independent transaction boundaries with periodic commit, (C) Write-ahead log for sinks |
| **Decision** | Option A |
| **Rationale** | Checkpoint = the consistency boundary. If we commit transactions at checkpoint boundaries, recovery always rolls back to a consistent state. Independent transactions create gaps between checkpoint and transaction boundaries. |
| **Trade-offs Accepted** | Transaction duration = checkpoint interval. Long checkpoint intervals mean long-held transactions (problematic for some external systems with lock contention). |
| **Revisit Trigger** | If users report lock contention issues with checkpoint intervals > 1 minute. |

### Decision 2: Idempotent Commit requirement

|  |  |
| -- | -- |
| **Context** | The Commit notification may be delivered more than once (coordinator crash/restart). |
| **Options Considered** | (A) Require sinks to handle idempotent Commit, (B) Coordinator tracks Commit delivery with ACK, (C) Exactly-once Commit delivery via Raft log |
| **Decision** | Option A |
| **Rationale** | Simplest. Pushing idempotency to the sink avoids complex coordinator-side exactly-once delivery. Most external systems support idempotent operations natively or via a metadata tracking table. |
| **Trade-offs Accepted** | Sink implementors must think about idempotency. |
| **Revisit Trigger** | If sink idempotency proves too burdensome for custom connector authors. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | PreCommit times out (external system slow) | Checkpoint times out → Job enters FAILING → Abort + restart from last checkpoint | Checkpoint lost, brief delay | Medium |
| 2 | Commit succeeds but worker crashes before ACK | On restart, Coordinator re-sends Commit(N). Sink Commit is idempotent → no duplicate data. | No impact | Low |
| 3 | External system down during Commit | Sink retries with backoff. If exhausted, job FAILING. On recovery, Coordinator re-attempts Commit. | Delayed commit | High |
| 4 | External system transaction timeout | If the external system's transaction timeout is shorter than the checkpoint interval, the transaction may be aborted externally. Solution: align timeouts or decrease checkpoint interval. | Data loss if misconfigured | High |
| 5 | Mixed transactional and non-transactional sinks | Non-transactional sinks get at-least-once (may see duplicates). Transactional sinks get exactly-once. Documented as expected behavior. | Partial exactly-once | Low |

---

## 7. Security & Compliance

### 7.1 Transaction Credentials

* TransactionalSink implementations inherit the same authentication as the underlying Sink.
* Credentials support `${ENV_VAR}` substitution — never stored in plain text in pipeline configs.

### 7.2 Data Consistency

* The 2PC protocol guarantees that external system state is consistent with Wire's internal checkpoint state.
* Sinks that track committed checkpoint IDs provide an audit trail for consistency verification.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | 2PC state machine, phase transitions | Go `testing` | 100% of state transitions |
| Integration Tests | Full 2PC cycle with mock TransactionalSink | Go `testing` + mocks | Happy path + failure in each phase |
| Chaos Tests | Kill worker during PreCommit/Commit | toxiproxy + Docker | All failure scenarios in Section 6 |

### 8.1 Key Test Scenarios

1. Normal cycle: BeginTransaction → WriteBatch(×N) → PreCommit → Commit → verify data visible
2. Abort after PreCommit: BeginTransaction → WriteBatch → PreCommit → kill worker → restart → verify no data from aborted transaction
3. Idempotent Commit: Commit(N) called twice → verify no duplicate data
4. Recovery: Write 1000 records across 3 checkpoints → kill during checkpoint 3 → restart → verify exactly 2 checkpoints worth of data committed
5. Phase 2 failure: Commit fails → retry → verify eventual commit

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should we support cross-sink atomic commits (e.g., two sinks in one transaction)? | Tarun | Open — likely No for v1 |
| 2 | Should PreCommit have its own timeout separate from the checkpoint timeout? | Tarun | Open |
| 3 | Risk: External systems with short transaction timeouts may conflict with long checkpoint intervals. Need to document recommended configuration. | — | Acknowledged |
