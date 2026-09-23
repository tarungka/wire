# WIP-15 completion audit

This is an implementation checklist, not a completion claim. Preserve the
original README's requirements while verifying each against running code. This
branch builds on WIP-14's per-job policies and local worker runtime.

| Requirement | Current evidence | Remaining acceptance work |
| --- | --- | --- |
| Complete durable job/task state machine | `job_state_machine.go`, transition and task-status tests | Fix lifecycle gaps below; verify all transitions under storage failures and recovery |
| Submission, listing, inspection and filtering | Existing JSON graph-envelope handlers and CLI | YAML submission (WIP-19 integration); full detailed task/checkpoint response; malformed body/error audit |
| Binary submission | `handleSubmitBinary` returns 501 | Implement actual compiled-application submission and execution contract, including limits and isolation; do not call the existing stub complete |
| Cancel, including during deployment | Cancel commands and status handling exist | Paused cancellation, savepoint-before-cancel, durable retries and joined task termination |
| Pause and resume | Existing PauseJob immediately changes metadata after triggering; ResumeJob contains a redeployment TODO | Await completed savepoint, stop the old attempt, resume by deploying from the pinned savepoint; failure and coordinator-restart cases |
| Completed savepoints and restore/upgrade | Existing checkpoint manifests, worker restore, rescale and savepoint metadata | Cross-job compatible restore, operator identity validation and documented upgrade walkthrough |
| Savepoint lifetime | Explicit metadata deletion exists | Delete replica data safely; protect all live restore references; unfinished savepoint cleanup |
| Concurrent checkpoint and savepoint requests | Current trigger rejects overlap | Queue a user savepoint behind an active checkpoint as proposed; cancellation and recovery of queued requests |
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
