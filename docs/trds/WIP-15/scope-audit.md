# WIP-15 completion audit

This is an implementation checklist, not a completion claim. Preserve the
original README's requirements while verifying each against running code. This
branch builds on WIP-14's per-job policies and local worker runtime.

| Requirement | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Complete durable job/task state machine | `job_state_machine.go`, transition and task-status tests | Fix lifecycle gaps below; verify all transitions under storage failures and recovery |
| Submission, listing, inspection and filtering | Existing JSON graph-envelope handlers and CLI | YAML submission (WIP-19 integration); full detailed task/checkpoint response; malformed body/error audit |
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
