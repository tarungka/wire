# Technical Requirements Document (TRD)

> **Feature/Project:** `Two-Phase Commit for Transactional Sinks`
>
> **WIP ID:** `WIP-08`
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

Wire's execution-model.md states that exactly-once semantics for external sinks require "transactional or idempotent" sinks, but **never defines the two-phase commit (2PC) protocol**, the pre-commit/commit hooks, or how sink transactions integrate with the checkpoint lifecycle. Without this specification, it is impossible to implement exactly-once delivery to systems like Kafka (transactions) or PostgreSQL (SQL transactions).

### 1.2 Proposed Solution (Technical Summary)

Define a two-phase commit protocol where the checkpoint lifecycle drives the transaction lifecycle. Phase 1 (PreCommit) occurs when a checkpoint barrier arrives at a sink — the sink flushes all buffered data and prepares its transaction. Phase 2 (Commit) occurs when the Coordinator confirms global checkpoint completion — the sink finalizes the transaction. On failure, Abort rolls back any in-flight transaction and processing resumes from the last committed checkpoint.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define the 2PC protocol tied to checkpoint barriers | Distributed transactions across multiple sinks |
| Specify PreCommit, Commit, Abort semantics | Exactly-once for non-transactional sinks |
| Document integration with Kafka transactions | Cross-system atomic commits (e.g., Kafka + Postgres together) |
| Document integration with PostgreSQL transactions | Saga pattern or compensating transactions |
| Define failure recovery for each phase | Read-your-own-writes consistency |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| 2PC protocol specified | No specification | Complete protocol with sequence diagram | Doc review |
| Kafka exactly-once implementable from spec | No | Yes | Implementation test |
| Postgres exactly-once implementable from spec | No | Yes | Implementation test |

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
* **Technology:** Coordinator RPC (see WIP-05)
* **Interactions:** Sends TriggerCheckpoint to sources, receives AcknowledgeCheckpoint from all tasks, then broadcasts Commit notification.

**Component 2:** Sink Task Runtime
* **Responsibility:** Manages the TransactionalSink lifecycle within the checkpoint protocol.
* **Technology:** Go runtime wrapping the TransactionalSink interface (see WIP-02)
* **Interactions:** Calls PreCommit on barrier arrival, Commit on global completion notification, Abort on failure.

**Component 3:** TransactionalSink Implementation (per connector)
* **Responsibility:** Maps Wire's 2PC phases to the external system's transaction semantics.
* **Technology:** Connector-specific (Kafka transactions, SQL BEGIN/COMMIT, S3 multipart)
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

### 3.1 TransactionalSink Interface (from WIP-02)

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

### 3.2 Kafka Transaction Mapping

| Wire 2PC Phase | Kafka API Call |
|----------------|---------------|
| `BeginTransaction()` | `producer.BeginTransaction()` |
| `WriteBatch(events)` | `producer.Produce(records)` (within transaction) |
| `PreCommit(N)` | `producer.Flush()` — ensure all records are in Kafka broker buffers |
| `Commit(N)` | `producer.CommitTransaction()` — atomically commits all records |
| `Abort()` | `producer.AbortTransaction()` — discards all uncommitted records |

**Kafka-specific details:**
- Each parallel sink subtask gets a unique `transactional.id` = `{user-prefix}-{subtask-index}`.
- On recovery, Kafka's transaction coordinator automatically fences old producer instances with the same `transactional.id`.
- Consumer isolation level must be `read_committed` to avoid reading uncommitted records.

### 3.3 PostgreSQL Transaction Mapping

| Wire 2PC Phase | PostgreSQL Call |
|----------------|----------------|
| `BeginTransaction()` | `BEGIN` |
| `WriteBatch(events)` | `INSERT INTO ... VALUES (...)` (batched) |
| `PreCommit(N)` | `SAVEPOINT wire_chk_N` — flush all pending inserts |
| `Commit(N)` | `RELEASE SAVEPOINT wire_chk_N; COMMIT` |
| `Abort()` | `ROLLBACK` |

**PostgreSQL-specific details:**
- Long-running transactions can cause vacuum issues. Checkpoint interval should be kept reasonable (< 5 minutes) for Postgres sinks.
- For idempotent Commit: track committed checkpoint IDs in a metadata table `wire_checkpoints(sink_id, checkpoint_id, committed_at)`.

### 3.4 S3 Transaction Mapping

| Wire 2PC Phase | S3 API Call |
|----------------|-------------|
| `BeginTransaction()` | `CreateMultipartUpload()` |
| `WriteBatch(events)` | Buffer in memory / temp file; `UploadPart()` when buffer full |
| `PreCommit(N)` | `UploadPart()` for remaining buffered data |
| `Commit(N)` | `CompleteMultipartUpload()` — atomically makes file visible |
| `Abort()` | `AbortMultipartUpload()` — cleans up all parts |

**S3-specific details:**
- Multipart uploads have a 10,000 part limit. If exceeded, roll to a new upload within the same transaction.
- S3 lifecycle rules should be configured to clean up incomplete multipart uploads after 24 hours.

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
| **Trade-offs Accepted** | Transaction duration = checkpoint interval. Long checkpoint intervals mean long-held transactions (problematic for Postgres lock contention). |
| **Revisit Trigger** | If Postgres users report lock contention issues with checkpoint intervals > 1 minute. |

### Decision 2: Idempotent Commit requirement

|  |  |
| -- | -- |
| **Context** | The Commit notification may be delivered more than once (coordinator crash/restart). |
| **Options Considered** | (A) Require sinks to handle idempotent Commit, (B) Coordinator tracks Commit delivery with ACK, (C) Exactly-once Commit delivery via Raft log |
| **Decision** | Option A |
| **Rationale** | Simplest. Kafka transactions are already idempotent by ID. Postgres can check a metadata table. Pushing idempotency to the sink avoids complex coordinator-side exactly-once delivery. |
| **Trade-offs Accepted** | Sink implementors must think about idempotency. |
| **Revisit Trigger** | If sink idempotency proves too burdensome for custom connector authors. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | PreCommit times out (external system slow) | Checkpoint times out → Job enters FAILING → Abort + restart from last checkpoint | Checkpoint lost, brief delay | Medium |
| 2 | Commit succeeds but worker crashes before ACK | On restart, Coordinator re-sends Commit(N). Sink Commit is idempotent → no duplicate data. | No impact | Low |
| 3 | External system down during Commit | Sink retries with backoff. If exhausted, job FAILING. On recovery, Coordinator re-attempts Commit. | Delayed commit | High |
| 4 | Kafka transaction timeout (default 60s) | If checkpoint takes > 60s, Kafka broker aborts the transaction. Solution: increase `transaction.timeout.ms` or decrease checkpoint interval. | Data loss if misconfigured | High |
| 5 | Postgres long transaction causes deadlock | PreCommit should flush quickly. If deadlock detected, Abort and retry via checkpoint recovery. | Brief delay | Medium |
| 6 | S3 multipart upload exceeds 10,000 parts | Sink implementation must detect this and roll to new upload within same logical transaction. | Implementation complexity | Low |
| 7 | Mixed transactional and non-transactional sinks | Non-transactional sinks get at-least-once (may see duplicates). Transactional sinks get exactly-once. Documented as expected behavior. | Partial exactly-once | Low |

---

## 7. Security & Compliance

### 7.1 Transaction Credentials

* Kafka transactional producers require `transactional.id` permission (ACL: `WRITE` on `TransactionalId`).
* PostgreSQL transactions use the same connection credentials as normal writes.
* S3 multipart uploads require `s3:PutObject` and `s3:AbortMultipartUpload` permissions.

### 7.2 Data Consistency

* The 2PC protocol guarantees that external system state is consistent with Wire's internal checkpoint state.
* In case of doubt, the `wire_checkpoints` metadata table (for sinks that support it) provides an audit trail.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | 2PC state machine, phase transitions | Go `testing` | 100% of state transitions |
| Integration Tests | Kafka exactly-once: write → checkpoint → kill → verify no duplicates | Docker (Kafka), testcontainers | Happy path + failure in each phase |
| Integration Tests | Postgres exactly-once: write → checkpoint → kill → verify no duplicates | Docker (Postgres) | Happy path + failure in each phase |
| Chaos Tests | Kill worker during PreCommit/Commit | toxiproxy + Docker | All failure scenarios in Section 6 |

### 8.1 Key Test Scenarios

1. Normal cycle: BeginTransaction → WriteBatch(×N) → PreCommit → Commit → verify data visible
2. Abort after PreCommit: BeginTransaction → WriteBatch → PreCommit → kill worker → restart → verify no data from aborted transaction
3. Idempotent Commit: Commit(N) called twice → verify no duplicate data
4. Kafka: Produce records → checkpoint → consume with `isolation.level=read_committed` → verify exactly records from committed transactions
5. Postgres: INSERT rows → checkpoint → verify row count matches expected (no duplicates)
6. Recovery: Write 1000 records across 3 checkpoints → kill during checkpoint 3 → restart → verify exactly 2 checkpoints worth of data committed

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should we support cross-sink atomic commits (e.g., Kafka AND Postgres in one transaction)? | Tarun | Open — likely No for v1 |
| 2 | What is the maximum acceptable checkpoint interval for Postgres sinks before lock contention becomes a problem? | Tarun | Open |
| 3 | Should PreCommit have its own timeout separate from the checkpoint timeout? | Tarun | Open |
| 4 | Risk: Kafka's `transaction.timeout.ms` default (60s) may be too short for large checkpoints. Need to document recommended configuration. | — | Acknowledged |
| 5 | Risk: S3 eventual consistency may cause Commit to succeed but data not immediately visible. Acceptable? | — | Acknowledged |
