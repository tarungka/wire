# WIP-03 completion audit

Baseline: master `bac0fef`, including WIP-02 (#211). Follow-up to merged
#150 and #206. Status remains Partially Implemented until the requirements
below are implemented and verified through the distributed runtime.

## Required implementation and evidence

| Requirement | Baseline / completion evidence needed |
| --- | --- |
| Fixed job key-group count | Carry default 128 and validated power-of-two count (1–32768) through submission, persisted job graph, deployment and checkpoint/savepoint metadata. Reject parallelism above count and restore count mismatch. |
| Deterministic hashing and ownership | Existing Murmur3 and range library; verify nil/empty keys, boundary ownership, uneven parallelism, and one million keys with distribution bounds. |
| Consistent scheduler ranges | Scheduler now uses the shared half-open floor partition library and converts ends to the inclusive RPC convention. Configured-count and all-owner boundary tests cover deployment. |
| Distributed KeyBy routing | Physical task planning and registered KeyBy selectors now route across actual workers. Cluster shuffle tests and router barrier tests pass. Multiple output groups remain unsupported and need scope review. |
| Keyed state layout and ownership | Preserve big-endian key-group prefix, operator identity and namespace. Verify range scans, task ownership and isolation using real Pebble state. |
| Durable savepoint rescale | Complete savepoint before stopping old tasks; retain fixed group count; plan old/new ownership; fetch and merge only assigned state ranges; restore before processing with fenced deployment attempts. Failed transfer must not publish partial state or resume incorrectly. |
| Rescale integration | Real worker write → savepoint → rescale → read verification for 4→8, 8→4 and 4→3; verify every key exactly once, including group 50 and nil keys. |
| Documentation and PR | Correct contradictory examples, document actual configuration/protocol and limits, update status after validation, and open linked follow-up PR using personal account tarungka. |

## Specification inconsistencies to resolve

- The pseudocode `group * parallelism / count` is not the inverse of floor
  range boundaries when count is not divisible by parallelism. The library
  already adjusts for that boundary; routing must use the same ownership.
- Section 3.4's example/diagram assigns [16,32) to task 4 after 4→8, but the
  specified contiguous range formula assigns it to task 1. Group 50 belongs
  to task 3 after 4→8, as correctly stated in acceptance scenario 3.
- Preserve the intentional non-goals: no hot rescale without a savepoint,
  dynamic group splitting, weighted assignment or key-group count migration.

## Current verification (2026-09-14)

- Full coordinator and worker suites passed before the latest cluster fixture.
- `TestClusterSavepointRescalesKeyGroups` passes 4→8, 8→4 and 4→3
  using two workers, real replica RPC transfers and Pebble range restoration.
  It verifies every assigned group's stored value and excludes foreign groups.
  The fixture now uses typed state in a map downstream of a hash shuffle.
  Opaque source offsets and transactional sink redistribution are not covered.
- Coordinator tests verify old-attempt cancellation completes before new
  deployment and that snapshot fetch grants are persisted with assignments.
- Worker assembly tests reject invalid coverage, mixed state types, stale
  epochs and incompatible topology. Opaque nonempty state is rejected because
  no redistribution contract exists for those bytes yet.
- The HTTP rescale endpoint exists; malformed-body validation is tested.
  All three HTTP-to-cluster rescale scenarios pass with the race detector.
- Savepoints selected for active rescale recovery cannot be deleted until a
  replacement checkpoint is complete or the job is terminal.

Status remains Partially Implemented. Remaining verification includes broader race
coverage, recovery during rescale, keyed operator/source lifecycle coverage,
full lint and test gates, corrected specification examples and the follow-up PR.

The race-enabled cluster test exposed an identity-field race between rescale
and scheduler logging; rescale now updates only mutable job fields under the
coordinator lock. The rerun passed for all three parallelism changes. Lint
reported zero issues before the latest test extension.

Latest validation:

- `go test ./...` passed across the repository.
- The updated HTTP cluster test passes with `-race`, including a new checkpoint
  after each rescale and deletion of the now-unneeded old savepoint.
- `golangci-lint run ./...` reports zero issues; `git diff --check` passes.

The strengthened keyed-map cluster fixture passes all three HTTP rescale
scenarios with `-race`, including replacement checkpoint completion.
