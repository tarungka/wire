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
