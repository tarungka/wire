# WIP-03 completion audit

Baseline: master `bac0fef`, including WIP-02 (#211). Follow-up to merged
#150 and #206. Status remains Partially Implemented until the requirements
below are implemented and verified through the distributed runtime.

## Required implementation and evidence

| Requirement | Baseline / completion evidence needed |
| --- | --- |
| Fixed job key-group count | Carry default 128 and validated power-of-two count (1–32768) through submission, persisted job graph, deployment and checkpoint/savepoint metadata. Reject parallelism above count and restore count mismatch. |
| Deterministic hashing and ownership | Existing Murmur3 and range library; verify nil/empty keys, boundary ownership, uneven parallelism, and one million keys with distribution bounds. |
| Consistent scheduler ranges | Scheduler currently hard-codes 128, distributes remainder to early tasks and uses inclusive RPC ends; reconcile explicitly with library half-open floor boundaries. |
| Distributed KeyBy routing | Scheduler currently rejects shuffle edges. Build physical task/edge assignments, apply key selection, route by key-group owner, and preserve per-key order and broadcast control boundaries. Test actual workers. |
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
