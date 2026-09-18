# Transactional sink runtime contract

WIP-10 implementation is in progress. This document describes the current API and safety boundaries; it is not an exactly-once acceptance declaration. Remaining recovery and crash-test work is tracked in [implementation-plan.md](implementation-plan.md).

## Custom sink interface

Implement `sdk.TransactionalSink`: ordinary `Open`, `Write`, `Close`, plus `BeginTransaction`, `PreCommit`, `Commit`, `Abort`, `Checkpoint` and `RestoreCheckpoint`. It is structurally compatible with the engine's sink interfaces and can be returned directly by a registered worker sink factory. Cluster graphs refer to that factory through `AddSinkNamed`.

The runtime drains pre-barrier records, calls PreCommit with the checkpoint deadline, captures Checkpoint state, replicates it, and then reports checkpoint readiness to the coordinator. Checkpoint must serialize the prepared external transaction's recoverable identity. It must not commit that transaction. RestoreCheckpoint runs after Open; the runtime re-drives Commit for a globally completed prepared snapshot before reporting RUNNING or starting another transaction.

Commit must be idempotent across process restarts and lost responses. Its identity must include the job and logical sink task, not just checkpoint number. Calls retry five times with exponential waits starting at 100ms; cancellation interrupts waits. Connector operations must honor their context and configure external request deadlines. The retry limit bounds attempts, not an uncooperative external call's duration.

Close must release local resources without automatically rolling back an externally prepared transaction with an uncertain decision. Once a readiness report may have reached the coordinator, shutdown is not evidence of an abort decision. An explicit abort stops the task with ErrTransactionAborted; it does not begin another transaction or release queued records. Recovery must replay the rolled-back interval from a completed checkpoint. Ordinary checkpoint failure tolerance cannot authorize continuing after transactional rollback. Orphan reconciliation and recovery before the first completed checkpoint remain under implementation.

## Runtime support

The distributed worker TaskSlot uses the durable coordinator checkpoint decision. The SDK's embedded executor currently has no durable global checkpoint decision service: it rejects TransactionalSink before opening external resources. It must not silently treat one as an ordinary sink or commit based only on local EOF. Supporting durable embedded transactional execution requires checkpoint coordination/recovery for that executor; an adapter alone does not provide it.

Ordinary sinks keep their existing at-least-once behavior. Mixed pipelines do not gain atomic visibility across sinks or external systems. Compliant transactional sinks can apply a shared checkpoint decision at different times.

## Recovery, fencing and deployment

The checkpoint coordinator persists the captured deployment attempt. Commands and ACK/failure reports carry that identity; a replacement attempt rejects stale or missing identities. Prepared transactions also match the barrier epoch. These fences protect Wire's protocol; the connector must independently fence obsolete writers in its external system.

This protocol extension requires coordinated coordinator/worker upgrades: old coordinators omit attempt identity, which new workers correctly reject for named attempts. Do not weaken that check as a rolling-upgrade workaround. Stop/fence old execution before resuming on matching binaries; previously completed snapshots retain their stored restore identities.

External transaction retention must exceed the checkpoint interval plus worst-case checkpoint and recovery delays. If an external system expires a globally committed-but-not-yet-applied prepared transaction, Wire cannot recreate its effects from a skipped checkpoint without risking duplicates. Such loss must fail recovery visibly.

Credentials are connector configuration concerns. The transaction API does not redact or encrypt submitted job configurations; connector authors should resolve secret references in the worker and apply the repository's configuration/storage protections. Do not serialize credentials into transaction handles.
