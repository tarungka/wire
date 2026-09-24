# WIP-15 completion audit

This is an implementation checklist, not a completion claim. Preserve the
original README's requirements while verifying each against running code. This
branch builds on WIP-14's per-job policies and local worker runtime.

| Requirement | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Complete durable job/task state machine | `job_state_machine.go`, transition and task-status tests | Fix lifecycle gaps below; verify all transitions under storage failures and recovery |
| Submission, listing, inspection and filtering | Existing JSON graph-envelope handlers and CLI | YAML submission (WIP-19 integration); final detailed response acceptance; malformed body/error audit |
| Binary submission | `handleSubmitBinary` returns 501 | Implement actual compiled-application submission and execution contract, including limits and isolation; do not call the existing stub complete |
| Cancel, including during deployment | Durable scheduler reconciliation, old-attempt fencing, CLI teardown and recovery tests | Final whole-workflow acceptance |
| Pause and resume | Durable queued intent, atomic checkpoint/PAUSING decision, fenced teardown, RESUMING placement; live CLI restores offsets and keyed state | Final operational walkthrough and acceptance audit |
| Completed savepoints and restore/upgrade | Validated cross-job restore, atomic succession, protected references; CLI/HTTP live upgrade tests including two transactional upgrades | Additional crash-point acceptance and executable deployment walkthrough |
| Savepoint lifetime | Explicit metadata deletion exists | Delete replica data safely; protect all live restore references; unfinished savepoint cleanup |
| Concurrent checkpoint and savepoint requests | Durable FIFO queue, HTTP 202, actual runner, deletion/race/recovery tests | None identified in queue behavior; final acceptance remains |
| Automatic recovery | Worker-loss and checkpoint selection tests; WIP-14 per-job restart policy | End-to-end REST/CLI evidence, bounded budget and all-workers-lost cases; retain explicit FAILED status for exhaustion |
| Cluster status and node removal | Durable removed identities, admission fencing, lease-aware recovery and live HTTP removal test | Final operational walkthrough |
| Health, readiness and metrics | Existing endpoints and metrics listener | Include actual addresses/status semantics in the final API reference and walkthrough |
| Authenticated REST and protected secrets | WIP-17/WIP-19 dependencies | Verify all private routes and ensure resolved credentials never persist or appear in responses |
| CLI and operational walkthrough | Existing JSON submission and management commands | YAML/binary support; run complete documented lifecycle against live workers |
| Tests and final PR | Existing API and recovery suites | Full lifecycle, failure injection, all state transitions, end-to-end CLI and final race/build/vet/lint |

Compatibility decisions must be explicit. Existing numeric persisted job states,
JSON graph envelopes and explicit FAILED (rather than CANCELED) on exhausted
recovery remain supported. Do not replace executable workflows with metadata-only
success responses or remove an original requirement to fit existing behavior.

## Durability foundation

`TestJobTransitionsPublishOnlyAfterDurableWrite` injects a store failure for every
allowed transition. It reproduced the old behavior: failed persistence still
changed cached status, timestamps/recovery counters or released the job name.
Transitions now prepare a copy, persist it, then update the existing live job and
name reservation. The same operation can be retried without double-counting a
restart. The test checks stored/cache agreement after retry.

## Cancellation reconciliation

Cancellation now persists intent and returns a snapshot in CANCELING. A scheduler
pass aborts active checkpoints durably, retries old-attempt cancellation commands,
and publishes CANCELED only once tasks report terminal states or their execution
authority expires. Recovery preserves CANCELING and waits for the old epoch fence.
Created jobs with no assignments complete cancellation without waiting for tasks.
Paused, failing and finishing jobs also accept ordinary cancellation. Running jobs
can request a savepoint before cancellation using the workflow below.

`TestCLICancelWaitsForWorkerTeardown` uses the CLI, HTTP server, coordinator, real
workers and a source whose Close is deliberately held. It verifies CANCELING
while Close is blocked and CANCELED after teardown. Coordinator tests verify
command retry/fencing, aborted checkpoint ordering, persistence failures and
recovered cancellation. Other lifecycle requirements are tracked separately below.

## Durable queued savepoints

A savepoint request behind an active checkpoint now returns a stable ID with
IN_PROGRESS and queued=true instead of a conflict. Queued requests are persisted,
ordered by arrival, and receive priority over new automatic/final boundaries.
Activation atomically replaces the queued metadata with the checkpoint's identity;
a failed write retains the original request for retry. Deletion is rechecked
under the dispatch lock to prevent stale dispatch from recreating a canceled ID.
The checkpoint runner performs dispatch outside the placement scheduler and skips
store scans while a checkpoint is active. Recovery retains unstarted requests,
fails interrupted snapshots, and clears the old leadership term's active cache.

Tests cover the running checkpoint runner, HTTP 202 responses, FIFO completion,
recovery, activation persistence failure, deletion races and job cancellation.
The pause/resume implementation below builds on this queue.

## Savepoint-based pause/resume

Pause intent and its queued savepoint are written atomically. Checkpoint completion
atomically pins the restore boundary and changes the job to PAUSING. The shared
teardown reconciler waits for old task termination before PAUSED. Resume validates
the pinned checkpoint, enters RESUMING, and uses normal scheduler deployment
without spending a recovery attempt. A newer completed checkpoint releases the
pin. Failed pause snapshots report pause_failure without claiming suspension.

Unit tests cover failed request/resume writes, duplicate requests, invalid pinned
checkpoints, missing replicas and recovery of queued/pausing/paused/resuming states.
The live CLI test restores source offset 1 and keyed count 1, then emits count 2.
Its transactional variant loses the first response after an external commit and
verifies that resume produces each output once. These tests exercise real workers
and replicas. Remaining WIP-15 requirements in the table still gate completion.

## Savepoint before cancellation

The REST `cancel?savepoint=true` and CLI `jobs cancel --savepoint` persist the
snapshot and stop intent together. Completion atomically changes the job to
CANCELING; snapshot failure leaves RUNNING and reports the failure. Commit commands
are queued under the ownership lock before teardown commands can be enqueued.
This orders commands but does not claim an external commit acknowledgement.

Tests cover atomic request failure, duplicate/conflicting operations, recovery,
HTTP query validation, snapshot failure, and a live CLI/worker/replica run that
verifies a completed savepoint and source teardown before terminal cancellation.
Cross-job restore and transactional reconciliation remain part of the broader
restore/upgrade acceptance work above.

## Safe node removal

Node deletion persists a removal marker before updating admission. The marker
survives leadership recovery, rejects registration/heartbeats and excludes the
worker from new placements and replica selection. Retaining its last contact
lease prevents a replacement from racing execution on the removed process.
The scheduler fails affected active jobs and cancels their old assignments;
recovery waits for terminal reports or authority expiry. Removed identities
remain visible and require a new ID for a replacement process.

Unit tests cover write failure, idempotence, heartbeat and registration rejection,
stale plans, task acknowledgements, contact expiry and the previous epoch fence.
A live CLI/HTTP removal test recovers onto the remaining real worker, asserts source
teardown before replacement, and verifies exactly one recovery attempt. It uses
no checkpoint, so initial-position replay is expected; archive migration is not
claimed by node removal.

## Cross-job restore work in progress

The [upgrade implementation contract](savepoint-upgrade-contract.md) records the
archive identity, transaction lineage and reference-lifetime invariants still to
implement. The direct physical-layout planner maps target tasks to original
archive task IDs and rejects incompatible operator order/types, ownership,
channels and replica inventories. The coordinator now publishes successors atomically, authorizes source-to-target
archive transfer, preserves transaction identity and pins the original savepoint.
A real-worker upgrade restores offsets and keyed state while changing a Process
class; a transactional variant covers a lost commit response. HTTP/CLI submission, repeated transactional upgrades and deletion/submission
races now have tests. Additional crash-point acceptance and the full executable
walkthrough remain open. These checks do not establish complete upgrade acceptance.

## Public submission export

`StreamExecutionEnvironment.ExportSubmission` exposes the same validated envelope
builder as remote execution without executing or consuming the environment.
The registered-worker example's export mode produces a real CLI input file, and
its integration test submits that file through the CLI to real services. Tests
also compare exported and directly submitted graph/policy bytes, reject anonymous
operators and oversized envelopes, and verify repeated export leaves execution
available. This removes the need for applications to import internal RPC types
just to generate a CLI submission; the full savepoint deployment walkthrough
remains a separate acceptance item.

## Job task inspection

The job-detail endpoint now includes sorted assigned tasks with operator/subtask,
worker, attempt and observed status from a consistent coordinator snapshot.
Unreported status is UNKNOWN, including after leadership recovery; corrupt stored
assignments return an error rather than silently hiding tasks. The HTTP and
snapshot tests cover these fields. Per-task heartbeat metrics now include record/byte counters, backpressure and
the receipt timestamp. Ownership-fence tests reject other workers, jobs, epochs
and attempts, lost/removed workers and absent reports, and verify copied values.
Checkpoint summaries are described below; missing metrics are not fabricated as zeros.

## Durable checkpoint inspection

Job inspection reports persisted outcome counts and the highest completed ID,
separately from the recovery-selected checkpoint. Completion timestamps are
written with the checkpoint decision and duration is omitted for legacy records.
Tests cover duplicate ACK accounting, savepoint aborts, in-progress records,
manifest/pointer exclusion and corrupt history. The scan runs outside the global
ownership lock. Historical metadata retention must preserve these counts when
physical savepoint cleanup is added; final workflow acceptance remains open.

## Checkpoint API error envelope

Checkpoint/savepoint replica-unavailable responses now use the standard JSON
error envelope with CHECKPOINT_UNAVAILABLE and HTTP 503. Checkpoint lookup uses
INVALID_REQUEST (400), CHECKPOINT_NOT_FOUND (404) and INTERNAL_ERROR (500),
including corrupt metadata. Tests verify content type, machine-readable codes
and that unavailable triggers do not start a checkpoint. Submission rejection
also covers null/array bodies and malformed base64/msgpack without publishing a
job. The broader lifecycle acceptance and binary/YAML submission remain open.

## Recovery pin before physical cleanup

Deleting a completed savepoint now also checks whether it is the latest selected
recovery boundary of any active state of its source job. Previously only explicit
pause/rescale/upgrade references blocked deletion. Tests cover all nonterminal
states, release after a newer checkpoint, and terminal jobs. This is a prerequisite
for archive cleanup, not physical deletion itself: replica deletion delivery,
durable retry/tombstones and imported artifact lifetime still require work.

## Replica deletion foundation

`FileCheckpointStore.Delete` durably records an identity-specific deletion marker
before removing the inline snapshot and retained portable archive. Reads and late
publications reject that marker, including after reopening the store. Tests cover
idempotent deletion, unrelated epochs, concurrent publication, and deletion during
a held archive upload. Shared content-addressed Pebble artifacts remain until
reference-aware collection can establish that no checkpoint uses them. Coordinator
authorization, durable deletion delivery/retry and artifact collection are still
required before the HTTP delete operation can claim physical cleanup.

## Atomic cleanup requests

Savepoint deletion now atomically writes a hidden savepoint tombstone, the exact
replica cleanup inventory, and checkpoint invalidation. A failed batch publishes
none of these changes. Completed checkpoint records/manifests remain available for
outcome accounting and transactional recovery fencing; deleted savepoints vanish
from Get/List and cannot be reactivated as queued requests. Tests cover failed
publication, deletion retries and retention of the replica identity/epoch. Replica
delivery/receipts and shared-artifact collection remain pending, so physical space
reclamation is not yet claimed by the HTTP endpoint.

## Durable cleanup receipts

The coordinator exposes a certificate-guarded cleanup acknowledgement RPC with
the configured checkpoint acknowledgement timeout. It accepts only the exact
worker address, current coordinator epoch and persisted job/savepoint/task/archive
identity. Per-replica completion and the all-replicas completion time are durable;
a failed write remains retryable and duplicate receipts are idempotent. Tests
cover mismatched identities, removed/lost workers, write failure, partial and final
receipts. Command dispatch and worker receipt generation are still required; this
RPC alone does not initiate deletion or establish end-to-end cleanup acceptance.

## Cleanup command delivery

The joined checkpoint maintenance runner now retries durable cleanup requests
through worker commands every five seconds. Reads occur outside the coordinator
ownership lock; pending command queues are bounded and duplicate pending deletion
commands coalesce. Workers use a bounded, joined cleanup queue so disk operations
and receipt RPCs do not block heartbeat or deployment dispatch. Commands validate
current coordinator and exact replica identity before deletion. Lost/removed or
unavailable replicas remain pending for retry, not silently completed.

Coordinator tests verify retries until durable receipts. Worker RPC tests verify
stale-command rejection, durable deletion before receipt and duplicate handling.
The live SDK CLI cancellation/savepoint test now deletes the savepoint and checks
that real replica snapshot/archive files disappear and deletion markers remain.
Shared content-addressed Pebble artifact collection, cleanup observability and
additional leadership/crash acceptance remain open.

`TestCleanupDispatchRecoversWithoutInMemoryQueue` reconstructs a coordinator from
durable metadata, preserves completed replica receipts, waits for renewed worker
contact and emits only unfinished work with the new epoch. Full coordinator,
worker, RPC and SDK race suites pass for this delivery implementation.
