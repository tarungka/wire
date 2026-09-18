# WIP-10 acceptance — 2026-09-19

The distributed two-phase commit contract is implemented. This record separates runtime evidence from guarantees that each external connector must supply.

| Requirement | Evidence | Verified outcome |
| --- | --- | --- |
| Prepare before capturing a recoverable handle and ACK | `TestTransactionalSnapshotFollowsPrepareBeforeACK` | Preparation/snapshot failures cannot ACK; captured state contains prepared identity |
| Normal global commit, including bounded-source tail | `TestBoundedTransactionalJobCommitsFinalRecords` | Two sources, two sink tasks, network shuffle, replica uploads: 1,000 unique visible records and no unfinished transaction when FINISHED |
| Worker crash after preparation | `TestTransactionalRecoveryAfterProcessKillDuringThirdCheckpoint` | Actual OS worker process kill bypasses every cleanup defer; replacement restores checkpoint two and removes the third preparation |
| 1,000 records across three intervals | Same process test | 300 and 300 committed; 400 prepared then killed. Recovery exposes exactly 600 records before replay; completion exposes exactly 1,000 unique records |
| Lost external commit response / duplicate Commit | `TestTransactionalCommitRetriesLostResponseWithoutDuplicates` | Each sink applies commit but returns two errors; retries complete with exactly 1,000 unique records |
| Bounded exponential retry and exhaustion | `TestCommitRetriesSameDecision`, `TestCommitRetryExhaustionPreservesCause`, `TestCommitRetryCancellationInterruptsBackoff` | Same checkpoint identity, five default attempts, cancellation interrupts waiting, original cause preserved |
| Uncertain cleanup | `TestTransactionCleanupDoesNotAbortAfterACK`, `TestTransactionCleanupDoesNotAbortFailedCommit` | Task shutdown cannot turn a possible durable commit into local rollback |
| Coordinator restart decision | `TestTransactionalCommitDecisionSurvivesCoordinatorReopen` | Pebble close/reopen with all in-memory commands discarded preserves the prepared completed checkpoint and writer generation; only the next incomplete checkpoint is marked for abort |
| Replacement commit before processing | `TestRestorePreparedTransactionCommitsBeforeNewTransaction`, `TestFailedRecoveryCommitCannotReportRunning` | Restore handle before Commit; failed commit prevents RUNNING and new transaction |
| Orphan gaps and stale external writers | `TestRecoverOrphansPreservesOnlySelectedDecision` | On-disk ledger reopened across connector instances; only the selected completed boundary is retained, aborted gaps are not committed, obsolete generations are rejected |
| Wire protocol fencing | `TestCheckpointReportsAreFencedToDeploymentAttempt`, `TestCheckpointDecisionsRejectPreviousTaskAttempt`, `TestPreparedTransactionRejectsOtherEpochDecisions` | Wrong attempt/epoch cannot authorize a transaction decision |
| Ordered external authority | `TestDeploymentGenerationPersistsAcrossRecoveryAndRescale` | Persisted and delivered generation advances independently of restart-budget counters |
| Abort requires replay | `TestAbortDoesNotConsumePostBarrierRecords`, `TestClusterCheckpointTransactionalAbort` | Abort stops the attempt without releasing the next interval into a replacement transaction |
| Recovery before the first checkpoint | `TestRestartBeforeFirstCheckpointReplaysInitialBoundary` | Fresh fenced deployment uses initial source position, subject to normal restart budget |
| Final checkpoint admission/failure | `TestFinalCheckpointWaitsForAllSourcesAndBypassesMinPause`, `TestExhaustedSourceWaitsForFinalBoundary` | All sources must park; periodic minimum pause cannot block EOF; abort fails the attempt |
| Unsafe EOF | `TestPreparedTransactionDefersEOFButRejectsUncommittedTail` | Pending records without a final committed boundary cannot produce successful EOP |
| SDK capability preservation | `sdk/transactional_sink_test.go` | Worker factory compatibility, adapter preservation, embedded execution rejected before Open |

## Validation

- `go test -race -timeout 5m ./...`: passed, including all integration packages.
- The three worker acceptance tests for final output, lost commit responses and process kill each passed three additional race-enabled repetitions.
- The coordinator Pebble-reopen regression passed separately after the full-suite run.
- `go build ./...`, `go vet ./...`, repository-wide golangci-lint: passed. A final lint pass includes the added reopen regression.

## Boundaries

The external ledger fixtures persist through worker process termination; they do not simulate host power loss or certify any real database connector. The coordinator decision test reopens Pebble and drops queued commands; it is not a coordinated multi-host power-failure experiment. The existing cluster coordinator-failover suite also runs in the full race suite.

Exactly-once requires replayable source positions, durable external preparations, idempotent commits scoped to job/task, and atomic external generation fencing on every mutation. Wire cannot repair expired committed transaction handles or provide atomic visibility across external systems. Non-transactional sinks remain at-least-once.

The embedded executor has no durable global checkpoint coordinator and rejects transactional sinks. Rescaling a transactional sink without recoverable transaction-handle mapping is rejected. These support boundaries are explicit in the SDK/runtime contract; the protocol does not silently downgrade to ordinary writes.

Coordinator/worker binaries must be upgraded together for attempt identity, deployment generation and final-checkpoint support. Keep coordinator metadata and external fencing history; rolling metadata back behind an established external generation is not supported.
