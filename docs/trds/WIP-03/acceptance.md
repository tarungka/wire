# WIP-03 completion audit

Baseline: master `bac0fef`, including WIP-02 (#211). Follow-up PR
[#212](https://github.com/tarungka/wire/pull/212) extends merged #150 and #206.
Implementation is complete for the keyed-state scope specified below.
Final delivery additionally requires the current PR head to pass CI.

## Requirements and evidence

| Requirement | Implementation and verification |
| --- | --- |
| Fixed job key-group count | JobGraph, SDK configuration, task descriptors and savepoint/checkpoint metadata carry the count. Submission validates the power-of-two range 1–32768 and parallelism before persistence. `TestSubmitGraphKeyGroupsValidatedBeforePersistence`, `TestSavepointRetainsConfiguredKeyGroupCount`, and `TestCheckpointRestoreRejectsChangedKeyGroupCount` cover invalid input and restore mismatch. |
| Hashing and ownership | Murmur3 golden vectors, nil/empty keys, boundary/range consistency and one million random keys are tested in `internal/keygroup/keygroup_test.go`. Distribution passes the stricter 10% tolerance. |
| Scheduler ranges | Scheduler uses the shared half-open floor partition calculation, explicitly converting to inclusive RPC ends. Configured-count tests and physical task tests verify ownership and channel endpoints. |
| Distributed keyed routing | Registered KeyBy selectors and physical hash shuffles operate across workers. `TestClusterHashShuffleRoutesEveryRecord` and `TestClusterKeyBySelectsBeforeShuffle` verify every record and expected owner. Router tests cover ordered control boundaries. |
| Keyed state layout | Composite key encoding tests cover big-endian group prefix, operator ID, user key and namespace. `TestPebbleKeyGroupRangeBoundsAndCancellation` verifies bounded scans; `TestPebbleRescaleMergesOnlyAssignedRanges` checks merging and exclusion of foreign groups. |
| Savepoint rescale protocol | Savepoints complete only after durable checkpoint receipts. `RescaleJob` persists graph and savepoint selection before old-attempt cancellation. `TestRescaleJobStopsOldAttemptBeforeDeploying` proves new deployment waits for terminal old tasks and persists fetch grants. |
| Restoration safety | Worker assembly rejects incomplete coverage, mixed epochs/types and topology mismatch. `TestTaskSlotRescaleRestoreLifecycle` verifies restoration before processing. `TestPebbleRescaleFailurePreservesPublishedState` verifies failed/canceled restoration preserves published state. Fetch authorization binds source snapshots to target task, worker, epoch and attempt. |
| Distributed rescale acceptance | `TestClusterSavepointRescalesKeyGroups` uses two workers, HTTP rescale requests, real replica RPC transfers and a Pebble-backed map downstream of hash shuffle. All 4→8, 8→4 and 4→3 cases pass with `-race`; every assigned group's value (including 0 and 50) is verified and foreign groups are excluded. A replacement checkpoint completes and releases the old savepoint. |
| Recovery metadata | The recovered variant of `TestRescaleDeploymentRequiresCompletedMatchingSavepoint` reloads coordinator metadata and rebuilds restore instructions while preserving the old savepoint identity. This is a metadata recovery test, not a process-failure injection test. |
| Documentation and PR | The inverse assignment formula and 4→8 diagram are corrected. HTTP request, asynchronous lifecycle and supported state contract are documented. PR #212 was opened with personal account `tarungka`. |

## Supported contract and scope

WIP-03 specifies keyed Pebble state redistribution. Operators provide typed
snapshot handles and implement `KeyGroupStateRestorer`; the runtime cannot
infer how arbitrary opaque bytes should be partitioned, so nonempty opaque
state fails restoration explicitly. It does not claim generic source-offset
or transactional-sink redistribution. Stateless operators require no restore.

The endpoint preserves source/sink counts and their entire Forward-connected
groups, and changes shuffle-separated processing groups. Global requests that
change no operator are rejected without restarting the job. Explicit operator counts
are available through the `operators` map. Operator identities/configuration and
the fixed key-group count remain unchanged. See [rescale safety](../../rescale-safety.md)
for rollback, capacity, and restore admission behaviour.
Multiple downstream routing groups are not supported by the current physical
planner; general DAG fan-out is not a goal listed in WIP-03. These limits are
stated in the PR rather than counted as implemented capabilities.

The explicit non-goals remain unchanged: no hot rescale without a savepoint,
dynamic group splitting, weighted assignment or key-group count migration.

## Validation

- `go test ./...` passed across the repository.
- The latest keyed-map HTTP rescale fixture passed with `-race` for all three
  sizes, including replacement checkpoints and old-savepoint deletion.
- Subsequent coordinator recovery and corrupt-topology regression tests pass.
- `go test -race ./internal/coordinator ./internal/engine ./internal/worker`
  passes across all three affected packages.
- `golangci-lint run ./...` reports zero issues; `git diff --check` passes.
- GitHub CI for PR #212 is still running. Completion is not claimed until
  the current head's required checks finish successfully.
