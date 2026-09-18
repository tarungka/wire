# WIP-10 completion plan

Based on current master, with a separate `codex/wip-10-complete` branch. WIP-09 #222/#223 remain open and are not silently included in this PR. Status remains Partially Implemented until acceptance is verified.

## Requirements and evidence to produce

1. Prepare the sink before capturing its recoverable checkpoint state; snapshot and replica receipt must precede ACK. Test preparation failure, snapshot failure and post-barrier input ordering.
2. Persist and recover global commit/abort decisions. Completed checkpoints must re-drive idempotent sink commit before new writes after restore. Never abort a transaction that may have a durable commit decision merely because the worker lost contact. Define recovery of orphaned pre-commit transactions and stable job/task transaction identities.
3. Fence decisions by checkpoint, coordinator epoch and deployment identity. Delayed or duplicate decisions cannot affect a newer transaction. Preserve the existing prohibition on falling back past potentially committed transactional output.
4. Retry commit with bounded exponential backoff and cancellation. Exhaustion fails the task while preserving the durable commit decision for recovery; distinguish commit failure from abortable pre-commit failure.
5. Support custom transactional sinks through the public SDK and worker registry without adapters erasing transactional capabilities. Document idempotency, recoverable transaction handles, external timeout/fencing requirements, mixed sink guarantees and credential handling accurately.
6. Cover normal Begin/Write/Prepare/Commit, abort and replay after worker loss, duplicate commit, 1,000 records over three checkpoints with failure during the third, commit retries and coordinator restart. Use durable external-state fixtures and actual cluster paths; in-memory method counters alone do not establish exactly-once output.
7. Specify bounded-source final transaction behavior; no successful job may silently discard its uncheckpointed final records.
8. Run full race/integration/build/vet/lint, update the WIP/runtime contract and acceptance record, and publish a linked follow-up to #199 with passing CI using tarungka.

## Initial audit

The old status note is stale: worker TaskSlot execution already detects transactional sinks, reports checkpoint replication receipts, and consumes coordinator commit/abort commands. Existing manifests record SinkPrepared and SinkCommittedCheckpoint, but restore ignores those fields. Current operator snapshots precede PreCommit, so connector state cannot include the prepared transaction handle. Commit executes once without backoff, and deferred cleanup unconditionally aborts on exit, including uncertain post-ACK decisions. SDK Sink currently exposes no transactional contract. These are implementation gaps, not documentation-only work.

External transaction semantics cannot be invented by the runtime: the connector must durably identify prepared transactions, make repeated commits idempotent, and fence obsolete writers. The completion contract must explain and test that boundary, without claiming cross-system atomicity.

## Progress: recoverable preparation boundary

- Moved PreCommit before operator snapshot capture, while keeping ACK after successful snapshot capture and replica handling. Prepared transaction state can now be included in the archived sink snapshot.
- Added TestTransactionalSnapshotFollowsPrepareBeforeACK, covering success, failed preparation and failed snapshot. The fixture refuses to snapshot an unprepared transaction; ACK asserts snapshot completion.
- Targeted transactional tests and the complete engine package pass under -race. This does not yet establish recovery correctness; the remaining requirements above are still open.

## Progress: commit recovery and uncertain cleanup

- Commit retries the same checkpoint identity five times with 100ms exponentially increasing waits (1.5s total waiting), preserving the underlying cause on exhaustion. Cancellation interrupts waits; connector Commit must honor its context. No new transaction is begun by retry itself.
- Cleanup no longer aborts a prepared transaction once an ACK/upload may have reached the coordinator or commit has been attempted. Explicit abort decisions still abort; ordinary unreported transactions retain bounded independent cleanup. Connector Close is documented to preserve externally prepared transactions with uncertain decisions.
- Restoration of a globally completed SinkPrepared snapshot restores the operator handle and re-drives idempotent Commit before RUNNING or processing. The chain inherits the restored committed boundary. Prepared snapshots reject non-transactional replacement operators and inconsistent committed checkpoint metadata.
- Full engine race suite passes after runtime changes. Additional focused regressions prove failed recovery cannot report RUNNING, begin another transaction or abort prepared state; engine lint passes.
- Still open: coordinator/connector reconciliation of orphan transactions not represented by the selected snapshot, checkpoint/epoch/attempt decision fencing, abort-and-replay policy, terminal bounded-source data, SDK propagation and durable cluster/process acceptance tests. Preserving uncertain prepared state is necessary but is not complete orphan cleanup or an exactly-once acceptance claim.

## Progress: checkpoint decision ownership

- Checkpoint metadata captures the deployment AttemptID. Trigger/commit/abort payloads carry it, worker reports echo it, and both coordinator ACK/failure handling and worker command delivery reject another attempt. Missing identity cannot authorize a command for a named current attempt.
- Prepared transactions record the barrier's epoch. Commit, abort and duplicate barrier controls from another epoch are ignored before touching the transaction or checkpoint upload state.
- Added coordinator and worker regressions for missing/old/current attempts, and engine regressions for stale and future decision epochs. Existing fixtures now specify the epoch they actually prepared.
- This changes checkpoint protocol compatibility: a new worker will reject an old coordinator's unfenced commands for a named attempt. Deployment/upgrade guidance must cover this explicitly; do not silently weaken the check to permit missing identity.

## Progress: SDK contract and runtime capability boundary

- Added sdk.TransactionalSink with transaction hooks and prepared-handle checkpoint/restore methods. It fits registered worker sink factories structurally; the adapter preserves its concrete capabilities and ordinary sinks remain non-transactional.
- The embedded executor passes no checkpoint coordinator or transaction ACK path. It now rejects transactional sinks before Open rather than silently executing them as ordinary sinks. This is a documented runtime support boundary, not a claim that embedded checkpoint coordination has been implemented.
- Added SDK adapter, worker-factory compatibility and fail-fast tests. The full SDK/connectors race suite and SDK lint pass.
- Added runtime-contract.md and corrected the WIP's stale claim that cluster checkpoint wiring is absent. Remaining distributed recovery and acceptance work is still explicit; no completion status or PR publication yet.

## Progress: abort-and-replay and preparation timeout

- Explicit transactional abort now returns ErrTransactionAborted after one successful Abort. It neither begins a replacement transaction nor drains queued/alignment-buffered records. Deferred cleanup does not abort it a second time. The worker reports task failure so coordinator recovery can replay the rolled-back interval.
- PreCommit receives a deadline derived from checkpoint alignment start and the configured checkpoint timeout; deadline errors retain their cause and cannot produce an ACK.
- Full engine race suite passes. The worker suite identified an old test that explicitly expected continued writes after rollback; its transactional branch now requires failure/recovery, while the non-transactional tolerance branch remains unchanged. Transactional abort/commit and ordinary failure-threshold cluster tests pass three race repetitions; engine/worker lint passes.
- The abort fixture has no prior completed checkpoint, so it safely fails rather than silently continuing. Recovery before the first completed checkpoint and orphan resolution are still open; this test does not prove complete replay acceptance.
