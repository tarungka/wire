# WIP-06 implementation and acceptance record

Validated on 2026-09-15. This record maps the WIP requirements to the runtime implementation and its regression evidence.

## Storage and publication

The JSON manifest is stored at the logical key `jobs/{job}/checkpoints/chk-{id}/metadata.json` in the coordinator's existing durable metadata store. It is written in the same atomic batch as the completed checkpoint decision and latest checkpoint pointer. It is not a separately fsynced file alongside the coordinator database: splitting those writes would introduce a second publication boundary.

Retaining portable archives adds storage alongside imported backend artifacts; this follows the existing checkpoint retention lifecycle. Workers retain the exact replicated `checkpoint.archive` bytes under the existing identity-hashed replica storage layout. Manifest state paths are portable task namespaces, resolved by the authenticated checkpoint fetch service, rather than absolute paths on a particular replica. Archive members contain the task snapshot and any portable backend artifacts. SHA-256 and size checks bind downloaded state to the completed manifest before import; backend imports verify their own manifests and file contents.

A new checkpoint requires every assigned task's inventory. The manifest builder checks the captured physical topology, complete subtask coverage, disjoint/full key-group ownership, and chain membership. RPC key-group ranges are inclusive; persisted schema ranges are half-open. The physical primary can be the first non-source operator in a fused chain.

Source offsets are referenced by archive member and snapshot field, preserving opaque connector state without copying arbitrary source state into the human-readable manifest. Prepared transactional sinks record their last committed checkpoint and PRE_COMMITTED state; an external transaction ID is optional because the current transactional sink interface identifies commits by checkpoint ID.

## Recovery and compatibility

Ordinary recovery selects one completed checkpoint for the entire job. Fallback is refused if a skipped completed checkpoint has a prepared transactional sink: completion authorizes commit, which may already have reached the external system even though no commit receipt is stored. Such jobs fail rather than replay across that boundary and duplicate output. `LatestCheckpoint` tracks the selected recovery boundary and can move backwards for nontransactional fallback; it is not a high-water mark. Missing or corrupt manifests and explicitly invalidated state are skipped in descending checkpoint order. Unsupported schema versions stop recovery with an upgrade error. Metadata-store I/O errors are retried rather than interpreted as corrupt checkpoint contents. Exhausting all valid checkpoints fails the job instead of silently cold-starting or retrying forever.

Missing-state and digest-mismatch reports are bound to the current worker, task, epoch, and attempt. Persisted recovery grants identify which checkpoint the failed deployment was restoring; stale task failures cannot invalidate another deployment's state. A confirmed unusable archive refunds its deployment's recovery-attempt charge exactly once, persisted atomically with invalidation; searching older candidates does not exhaust the execution-failure budget. Transient I/O, permission and cancellation errors retain the same checkpoint for retry and use the normal recovery policy. Invalidation remains permanent; restoring replica files alone does not clear it. Other tasks are cancelled before a replacement deployment begins. Rescale savepoints are pinned; an invalid requested savepoint is not silently replaced by an unrelated checkpoint, and existing rescale rollback restores the prior job configuration.

Rolling upgrades must upgrade **all workers before the coordinator**. Old coordinators tolerate the additional manifest fields from upgraded workers. The new coordinator requires manifests from every checkpoint acknowledgement; upgrading it first prevents checkpoints from completing while any old worker participates. Pause automatic upgrades that would violate this order; do not weaken manifest validation for mixed workers.

Records written before WIP-06 have no manifest version and keep the existing recovery path. New records require the versioned manifest and archived bytes. Version 1 JSON readers accept unknown optional fields; unsupported schema versions are rejected. This does not promise an older binary can execute newly introduced task-state formats during a rolling downgrade.

Filesystem manifest validation uses contained `os.Root` opens, rejects non-regular entries and symlinks, and verifies byte counts and optional SHA-256 digests. Later consumers must also use contained opens; a validation pass cannot secure a subsequent arbitrary path-based open.

## Evidence collected

- `TestCompleteCheckpointCoverage`, `TestCompleteCheckpointChainedOperator`: complete coverage, ownership gaps/overlaps, chain consistency, shared/aliased directories.
- `TestCheckpointManifestFiles`: missing, truncated, same-size corrupt, and symlink state files.
- `TestCheckpointManifestPhysicalTopology`: manifest creation from actual fused physical tasks and JSON round trip.
- `TestClusterCheckpointReplicatesAndCompletes`, `TestClusterCheckpointTransactionalCommit`: durable JSON inventory after all ACKs, offsets and sink preparation metadata.
- `TestTaskCheckpointArchiveIncludesPortableArtifact`: byte-identical retained archive after backend relocation and retry.
- `TestClusterCheckpointRestartsFromReplica`: restore from manifest-approved state before source reads.
- `TestRecoverySelectsEarlierValidManifest`: missing/corrupt metadata, invalid state, and unsupported schema behavior.
- `TestClusterCheckpointFallsBackFromMissingArchive`: newest archive unavailable, entire job restores the previous checkpoint.
- Existing rescale and coordinator failover tests passed with the race detector after manifest integration.

## Final verification

- Full `go test -race ./...` passed, including coordinator, engine, worker, RPC and SDK suites.
- After the final completion-record consistency check, real-worker restart and corrupt-archive fallback tests passed with `-race` again.
- `TestRecoveryDoesNotReplacePinnedSavepoint` and `TestCheckpointInvalidationUsesCurrentRecoveryGrant` cover pinned selection and stale worker/task/epoch/attempt rejection.
- `TestClusterCheckpointFallsBackFromCorruptArchive` covers same-size corruption through the production download path.
- `golangci-lint run ./...` reported zero issues; `git diff --check` passed.

No public HTTP endpoint was added for the manifest. The current HTTP server has no authentication middleware; inspecting stored JSON uses the existing metadata-store interface. This WIP defines the manifest and its runtime use, not a new public inspection service.

## Review regression coverage

- `TestRecoveryNeverSkipsPossibleCommittedSink` and `TestClusterCheckpointRefusesTransactionalFallback`: fail closed across a possibly committed transaction.
- `TestInvalidCheckpointRefundsOnlyItsDeployment` and `TestClusterCheckpointSkipsFourMissingArchives`: durable, idempotent budget refunds and recovery past more bad candidates than the default attempt limit.
- `TestCheckpointFetchErrorPreservesTransientFailures` and `TestCancelledArchiveLoadDoesNotInvalidateCheckpoint`: only missing/corrupt state is classified as permanently unavailable.
