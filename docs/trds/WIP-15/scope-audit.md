# WIP-15 completion audit

This is an implementation checklist, not a completion claim. Preserve the
original README's requirements while verifying each against running code. This
branch builds on WIP-14's per-job policies and local worker runtime.

| Requirement | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Complete durable job/task state machine | `job_state_machine.go`, transition and task-status tests | Fix lifecycle gaps below; verify all transitions under storage failures and recovery |
| Submission, listing, inspection and filtering | Existing JSON graph-envelope handlers and CLI | YAML submission (WIP-19 integration); full detailed task/checkpoint response; malformed body/error audit |
| Binary submission | `handleSubmitBinary` returns 501 | Implement actual compiled-application submission and execution contract, including limits and isolation; do not call the existing stub complete |
| Cancel, including during deployment | Durable scheduler reconciliation, old-attempt fencing, CLI teardown and recovery tests | Savepoint-before-cancel and final whole-workflow acceptance |
| Pause and resume | Existing PauseJob immediately changes metadata after triggering; ResumeJob contains a redeployment TODO | Await completed savepoint, stop the old attempt, resume by deploying from the pinned savepoint; failure and coordinator-restart cases |
| Completed savepoints and restore/upgrade | Existing checkpoint manifests, worker restore, rescale and savepoint metadata | Cross-job compatible restore, operator identity validation and documented upgrade walkthrough |
| Savepoint lifetime | Explicit metadata deletion exists | Delete replica data safely; protect all live restore references; unfinished savepoint cleanup |
| Concurrent checkpoint and savepoint requests | Durable FIFO queue, HTTP 202, actual runner, deletion/race/recovery tests | Integrate queued savepoints into the pending pause workflow |
| Automatic recovery | Worker-loss and checkpoint selection tests; WIP-14 per-job restart policy | End-to-end REST/CLI evidence, bounded budget and all-workers-lost cases; retain explicit FAILED status for exhaustion |
| Cluster status and node removal | Existing cluster routes | Removal must stop/fence execution and recover affected jobs safely; current removal only deletes worker metadata |
| Health, readiness and metrics | Existing endpoints and metrics listener | Include actual addresses/status semantics in the final API reference and walkthrough |
| Authenticated REST and protected secrets | WIP-17/WIP-19 dependencies | Verify all private routes and ensure resolved credentials never persist or appear in responses |
| CLI and operational walkthrough | Existing JSON submission and management commands | YAML/binary/restore/cancel-with-savepoint support; run complete documented lifecycle against live workers |
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
Paused, failing and finishing jobs also accept cancellation; savepoint-before-cancel
is still outstanding.

`TestCLICancelWaitsForWorkerTeardown` uses the CLI, HTTP server, coordinator, real
workers and a source whose Close is deliberately held. It verifies CANCELING
while Close is blocked and CANCELED after teardown. Coordinator tests verify
command retry/fencing, aborted checkpoint ordering, persistence failures and
recovered cancellation. This does not complete pause/resume or the other rows.

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
This supplies the queue needed for real pause/resume; it does not implement that
workflow by itself. Legacy pause behavior remains unchanged until its replacement.
