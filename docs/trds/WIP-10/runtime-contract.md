# Transactional sink runtime contract

This is the implemented distributed WIP-10 contract. See [acceptance.md](acceptance.md) for verified behavior and test boundaries. Exactly-once output requires a replayable source and a connector that honors the external fencing, durability and idempotency requirements below.

## Custom sink interface

Implement `sdk.TransactionalSink`: ordinary `Open`, `Write`, `Close`, plus `RecoverTransactions`, `BeginTransaction`, `PreCommit`, `Commit`, `Abort`, `Checkpoint` and `RestoreCheckpoint`. It is structurally compatible with the engine's sink interfaces and can be returned directly by a registered worker sink factory. Cluster graphs refer to that factory through `AddSinkNamed`.

The runtime drains pre-barrier records, calls PreCommit with the checkpoint deadline, captures Checkpoint state, replicates it, and then reports checkpoint readiness to the coordinator. Checkpoint must serialize the prepared external transaction's recoverable identity. It must not commit that transaction. RestoreCheckpoint runs after Open; the runtime re-drives Commit for a globally completed prepared snapshot before reporting RUNNING or starting another transaction.

Commit must be idempotent across process restarts and lost responses. Its identity must include the job and logical sink task, not just checkpoint number. Calls retry five times with exponential waits starting at 100ms; cancellation interrupts waits. Connector operations must honor their context and configure external request deadlines. The retry limit bounds attempts, not an uncooperative external call's duration.

Close must release local resources without automatically rolling back an externally prepared transaction with an uncertain decision. Once a readiness report may have reached the coordinator, shutdown is not evidence of an abort decision. An explicit abort stops the task with ErrTransactionAborted; it does not begin another transaction or release queued records. Recovery must replay the rolled-back interval from a completed checkpoint. Ordinary checkpoint failure tolerance cannot authorize continuing after transactional rollback. Before the first completed checkpoint, bounded recovery retries reopen sources at their configured initial position and reconcile sinks at boundary zero. Source implementations must replay that initial position reliably and must not independently acknowledge source offsets before a global checkpoint decision. Non-replayable sources cannot provide exactly-once delivery.

## Runtime support

The distributed worker TaskSlot uses the durable coordinator checkpoint decision. The SDK's embedded executor currently has no durable global checkpoint decision service: it rejects TransactionalSink before opening external resources. It must not silently treat one as an ordinary sink or commit based only on local EOF. Supporting durable embedded transactional execution requires checkpoint coordination/recovery for that executor; an adapter alone does not provide it.

Ordinary sinks keep their existing at-least-once behavior. Mixed pipelines do not gain atomic visibility across sinks or external systems. Compliant transactional sinks can apply a shared checkpoint decision at different times.

## Recovery, fencing and deployment

The checkpoint coordinator persists the captured deployment attempt. Commands and ACK/failure reports carry that identity; a replacement attempt rejects stale or missing identities. Prepared transactions also match the barrier epoch. These fences protect Wire's protocol; the connector must independently fence obsolete writers in its external system.

This protocol extension requires coordinated coordinator/worker upgrades: old coordinators omit attempt identity, which new workers correctly reject for named attempts. Do not weaken that check as a rolling-upgrade workaround. Stop/fence old execution before resuming on matching binaries; previously completed snapshots retain their stored restore identities.

External transaction retention must exceed the checkpoint interval plus worst-case checkpoint and recovery delays. If an external system expires a globally committed-but-not-yet-applied prepared transaction, Wire cannot recreate its effects from a skipped checkpoint without risking duplicates. Such loss must fail recovery visibly.

Credentials are connector configuration concerns. The transaction API does not redact or encrypt submitted job configurations; connector authors should resolve secret references in the worker and apply the repository's configuration/storage protections. Do not serialize credentials into transaction handles.

### Orphan recovery and ordered writer authority

Distributed transactional sinks additionally implement `RecoverTransactions(context.Context, sdk.TransactionRecovery)`. After restoring the selected checkpoint handle, Wire calls this hook before Commit, BeginTransaction, RUNNING, or input processing. The hook fences the previous writer and aborts uncommitted external transactions other than the selected `CompletedCheckpointID`. Zero means no completed checkpoint; no old transaction is authorized for commit. Never interpret all checkpoint IDs less than the selected ID as commit decisions: aborted checkpoints leave gaps.

`DeploymentGeneration` is a per-job monotonically increasing value persisted atomically with each deployment, including rescale deployments. Combined with job/task identity it supplies an ordered external fencing token; AttemptID identifies a retry of the same authority. The connector must reject a lower generation, or the same generation with a different attempt, in the external system. Random attempt IDs alone cannot order delayed recovery calls. Every external mutation must check the established fence; merely fencing once in local memory is insufficient. Coordinator metadata rollback to an older generation requires separate external reconciliation and is not a supported recovery procedure.

Recovery must be idempotent across partial cleanup and repeated process crashes. Preserve already committed output and the selected prepared handle for idempotent Commit. Transactional rescale without a recoverable transaction-handle mapping is rejected before orphan cleanup; repartitioned operator state is not authorization to abort globally completed transactions.

### Bounded-source completion

A source with distributed checkpoint replication parks at EOF and reports FINISHING while remaining available for checkpoint triggers. Once every source in the active assignment has parked, the coordinator allocates a final checkpoint, exempt from minimum periodic pause. Sources snapshot their exhausted position and send EOP only after forwarding that barrier; tasks retain pending uploads, and prepared sinks retain EOP until the global commit decision. No local EOF authorizes a sink commit. Failure of this final checkpoint fails the attempt and requires replay. Recovery from its completed snapshot first replays its idempotent commit, then can complete an empty final interval.

Source watermark production stops at EOF before the final snapshot. A sink with records written since its last committed boundary must never report successful EOF: lack of a final coordinated boundary is an error. Distributed transactional sinks require checkpoint replication even when a task otherwise needs no restored state.
