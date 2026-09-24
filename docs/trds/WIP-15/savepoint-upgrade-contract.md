# Cross-job savepoint restore implementation contract

This document records the restore contract and remaining acceptance work.
The coordinator submission method, archive transfer, transaction lineage and
reference protection are implemented and exposed by REST submission and
`wire jobs submit --savepoint PATH`. The CLI reference describes the workflow;
the complete executable deployment walkthrough remains pending.
The completion audit still gates WIP-15.

## Submission and compatibility

An upgrade submits a new job with a completed savepoint from a stopped predecessor.
Validate the graph, operator IDs, state layout, archive inventory and durable
replica references before reserving the new name or publishing a CREATED job.
A failed validation or metadata write must leave both jobs and the savepoint
unchanged. Publish the new job, restore reference and predecessor succession
record in one metadata batch, under the same lock used by savepoint deletion.

For unchanged parallelism, map operator/subtask identity across job namespaces.
The physical task's ordered operator chain defines positional source/operator
state in the archive; matching only the task's primary operator is insufficient.
Require matching operator IDs/types/order, key-group ownership and channel layout.
Code and application configuration can change while keeping those identities;
the application remains responsible for compatible state serialization. A restore
error must fail deployment, never silently start from empty state.

The direct-restore planner does not redistribute opaque snapshots. Parallelism
changes require the existing explicit rescale state plan and its state-restorer
validation; rejecting an unsupported layout before stopping execution is required.
The original rescale and upgrade requirements remain in the completion audit.

## Archive identity and fetch authorization

Preserve source job, source task, checkpoint and epoch in the archive reference.
Do not rewrite the manifest or import it under a fabricated target identity.
Transport fetch requests need separate source archive and target deployment
identities. Authorization must verify:

- the exact completed savepoint/inventory and recorded replica;
- the new job's durable restore reference and selected checkpoint;
- the new task's assigned worker, current epoch, attempt and permitted source task;
- the source savepoint has not been deleted or invalidated.

A caller knowing an old archive path must not gain permission to fetch it.
Validate the checksum and size before importing under the original identity, then
pass state to the target operator chain with its validated layout.

## Transactional identity and succession

An external sink may have committed the source savepoint already. A new job ID
must not create an independent transaction namespace that replays that decision.
Carry the original transaction job/task identity separately from the new runtime
job/task IDs, advance the deployment generation and continue checkpoint IDs above
the predecessor's high-water mark. Recovery must reconcile the saved commit
before processing new records, using the existing idempotent sink contract.

The predecessor must no longer execute and must not have a later committed
boundary than the requested savepoint. Persist a single successor decision so
concurrent submissions cannot both become writers for the same lineage. An
upgrade must retain these guards across coordinator restart and repeated upgrades.
An uncertain submission response must not allow a second successor to execute.

## Reference lifetime and recovery

A created/deploying/running successor pins its source savepoint until it has a
completed checkpoint of its own. The pin is durable and blocks metadata and
physical archive deletion. On failure before that boundary, use the exact source
savepoint again; do not silently choose an older checkpoint. Permanent archive
loss must be reported explicitly. Source metadata and transaction lineage remain
necessary for audit/fencing even after the archive pin can be released.

## Required acceptance evidence

- Incompatible graph/chain/ownership rejected before publication.
- Failed submission batch publishes neither successor nor restore reference.
- Concurrent restore submissions and delete/submit races preserve one owner.
- Fetch permits only the assigned target attempt and mapped source archive.
- CLI savepoint → cancel → upgraded submission restores source offset and managed
  keyed state on real workers, with changed application code/configuration.
- Transactional sink committed output appears once, including lost commit replies,
  coordinator recovery and a second upgrade in the same lineage.
- Source savepoint remains protected before the successor's first checkpoint and
  can be deleted safely after reference release.

## Current implementation evidence

`SubmitJobFromSavepoint` validates the latest completed boundary of a stopped
predecessor and atomically publishes the successor, source reference and succession
record. Fault-injection and concurrent-submission tests verify all-or-nothing
publication and one successor. Recovery retains the source pin. Checkpoint IDs
start above the predecessor's high-water mark, including unsuccessful attempts.

Fetch requests distinguish source archive IDs from target deployment IDs. The
coordinator checks both the durable restore reference and the exact persisted
task mapping. Denial tests cover stale attempts, wrong workers/jobs/tasks/epochs,
missing pins, changed successors and invalid/deleted savepoints. The worker imports
under the original archive identity, and the engine requires an explicit source
mapping before applying it to a new runtime task.

A live two-worker test creates a savepoint, cancels the old job, submits a new job
with a replacement Process class, and restores source offset 1 and keyed count 1
before emitting count 2. Its transactional variant loses a commit response and
asserts each visible output appears once while preserving external job/task
identity. These tests now run cancellation and upgraded submission through CLI/HTTP. A
second transactional upgrade preserves the same external identity and output
without duplication. A concurrent delete/submit test proves that either deletion
wins with no successor, or submission pins the savepoint and blocks deletion.
Additional crash points and a complete executable deployment walkthrough remain
acceptance work, not implied by those tests.
