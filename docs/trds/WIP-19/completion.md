# WIP-19 completion audit

This follow-up preserves the entire proposal, including hot reload and live
configuration changes. It is not complete. It is stacked on WIP-18 to use the
SDK, connector, security and state integrations from WIP-13 through WIP-18.

| Requirement | Current evidence and remaining work |
| --- | --- |
| Strict YAML schema | Existing single-document, known-field parser and graph validation; schema field-coverage tests. Full original field and invalid-input audit remains. |
| Transform types and expressions | All listed transforms compile through existing CEL and SDK graph code. YAML windows now format JSON aggregate results for downstream projection, and numeric aggregations validate JSON input through checked callbacks. The documented window→select shape is covered across all three window kinds, both backends and embedded/checkpoint-configured execution; numeric projection and atomic error tests cover sum/min/max. Distributed serialization, worker registration and execution remain required. |
| Connector availability | Caller-provided source/sink factories are validated before construction. WIP-16 connector registry and worker factory integration remain required. |
| Graph conversion | Existing SDK graph construction, forward references and cycle rejection. Validate shuffle semantics against current SDK and parallel execution. |
| Pipeline state backend | `spec.state_backend` accepts WIP-18 nested HashMap/Pebble configuration. Validation runs before connector construction; omitted HashMap limit is 256 MiB and explicit zero is unlimited. SDK override has precedence. Full CLI/pipeline/system precedence remains open. |
| Parallel/keyed/window execution | Instance-aware YAML factories now execute a three-partition CEL pipeline through embedded and local coordinator/worker runtimes. Private config copies, partition identity, factory errors and legacy guards have race tests. The 12-case `TestYAMLParallelKeyedWindowRuntime` matrix now verifies tumbling/sliding/session windows on HashMap/Pebble through embedded and checkpoint-configured worker execution. Every case combines three distinct original record keys under the CEL-selected key and verifies all accumulator contributions. Multiple-source/keyed-fan-out acceptance now passes with legacy factories, parallel instance factories and checkpoint-configured workers. Each branch receives every expected record once; selected keys remain in one partition. Recovery and mixed bounded/unbounded completion remain open. |
| Checkpoint and restart | Configured policies reach local coordinator/worker execution with fresh-instance factories. `TestYAMLPeriodicCheckpointRecoversTransactionalOutput` completes a periodic checkpoint, fails a sink after staging new output, restores source offsets into fresh connectors, and verifies exactly one external commit of each expected result. Three race repetitions cover a CEL map and managed HashMap/Pebble windows. Mixed-source completion and broader external deployment/lifecycle acceptance remain open. |
| File watching and validation | WatchPipelineFile detects stable content edits, validates the complete named-worker graph and invokes an application callback serially. Atomic replacement, invalid edits, reversion and fail-stop callback tests pass under race detection. Automatic migration callback and CLI watch integration remain required. |
| Graceful switchover | Drain old execution and start the validated replacement without overlapping ownership. Not implemented. |
| Topology changes | Savepoint-based migration and failure rollback. Not implemented. |
| Configuration-only changes | Apply parallelism and checkpoint interval changes without job restart, as specified. Existing APIs alone do not prove this behavior. Not implemented. |
| CLI and operations | Add executable pipeline submission/watch paths, examples and upgrade/security guidance. Audit all commands using the built binary. |

The historical Kafka/stdout example remains illustrative: it does not imply
that those connectors are bundled. No requirement above is removed because
another WIP already has a helper or because a parser test passes.

## Open source lifecycle issue

The existing worker sets `SourceExhausted` on every replicated source.
`sourceCheckpointInput.finish` parks until a final trigger, and the coordinator
requires every source to report FINISHING before allocating that trigger.
Consequently a bounded branch in a checkpointed job with an unbounded source
keeps its slot and output streams open indefinitely. Current multi-source
acceptance uses finite sources; it does not prove independent branch completion.
This must be resolved with correct treatment of finished tasks in later
checkpoints, not by merely releasing EOF and omitting their restore state.

## Periodic checkpoint recovery evidence

The source emits its first record, then returns empty batches while waiting for
a confirmed transactional commit. This ensures the failure happens after a
completed periodic checkpoint rather than relying on a sleep. The sink stages
new output and fails once; fresh source/sink instances must restore and finish.
The mapped case commits exactly `[11, 12]`. Window cases for HashMap and Pebble
commit one JSON count result containing both input records. A valid restored
window source offset can be 1 or 2 depending on whether another completed
checkpoint captured the unfired window before EOF; either case must preserve
the accumulator and commit the result once. All three cases pass three runs
under `-race` (14.340s total). This tests task recovery with live replica
workers, not replacement of a crashed OS process.

## Registered worker transform execution

The YAML graph now carries versioned transform definitions. Workers explicitly
register and compile all ten transform classes. Named source, sink and DLQ
bindings pass JSON configuration to application worker factories. Regression
coverage submits through coordinator HTTP and worker RPC, removes submitter
closures, and checks parallel CEL/key-by/window/projection output. A separate
case checks successful output plus malformed JSON delivered in a named DLQ
envelope. Malformed, oversized and incompatible definitions are rejected.

A separate HTTP YAML adapter now registers strict JSON factories under
`http-api.yaml.v1`, preserving the original MessagePack class. A coordinator/worker
integration test submits YAML with CEL and delivers to an HTTP endpoint.
This does not prove process isolation,
hot reload, CLI loading, state migration or live configuration updates.

## CLI YAML submission

`jobs submit --format yaml` uses an injected compiler to preserve the existing
HTTP/security/mutation path. The stock binary maps public HTTP connector types
to their JSON worker classes. The CLI regression decodes the submitted graph,
checks the savepoint override and proves an invalid candidate sends no request.
Stock node-mode workers install these factories in a private registry. An
integration test uses the actual runWorker entry point, submits YAML through
the CLI, sends an HTTP source record and verifies CEL-transformed HTTP output.
Automatic watch/reload and migration are still outstanding; custom SDK workers
explicitly register these classes.

## Live checkpoint interval primitive

The coordinator now accepts an authenticated operator-level PUT to a running
job's checkpoint-interval endpoint. A synchronous batch persists the changed
policy in both metadata and graph before publishing it in memory. Tests verify
scheduler eligibility, disabling, unchanged assignments/commands, consistent
persisted policy, failed-write rollback, strict HTTP input and role checks.
WatchLiveUpdates now connects validated interval-only file edits to this API. Parallelism updates
without restart, migration and rollback remain unimplemented.

## Reload classification

PlanUpdate distinguishes identical definitions and interval-only changes from
other deployment edits. It validates both complete submission graphs and checks
configured state backends, including stateless graphs where no backend appears
in operator descriptors. Tests cover interval, timeout, parallelism, expression,
connector, backend, name and combined changes without mutating the old pipeline.
Automatic migration is not implemented by this classification primitive.

## Replacement layout preflight

The coordinator shares physical layout validation between actual savepoint
restore and ValidateReplacementLayout. Preflight rejects incompatible task
ownership, chain identity, backend kind or channel layout without stopping the
running job or sending worker commands. Code/config changes can pass structural
validation; this does not prove serializer compatibility or archive health.
Actual restore still requires a completed durable savepoint and repeats checks.
Preflight is exposed as POST /api/v1/jobs/{id}/replacement/validate and the SDK
ValidateReplacement method, with HTTPS role acceptance coverage. It has not yet
been connected to automatic reload, and topology-changing migration is not
implemented by this unchanged-layout restore check.


The live-file regression sends interval edits and reverts through the HTTP
client, verifies invalid YAML causes no request, and verifies an expression edit
stops with migration-required without sending a lifecycle mutation. Broader
coordinator runtime tests separately prove interval updates leave deployments
unchanged. Full automatic migration/rollback and concurrent external-update
reconciliation remain open.

## In-place same-layout replacement primitive

ReplaceJobFromSavepoint now validates a latest completed savepoint and unchanged
physical layout before persisting a replacement under the existing job identity.
The fenced restart path joins old tasks first. A dedicated checkpoint pin keeps
full task snapshot restore (including opaque source offsets) separate from
key-group redistribution. Failed placement/deployment uses existing rollback
handling and restores original graph/policies and resolves its secret bindings.
A regression verifies full restore descriptors and policy rollback alongside
rescale tests. End-to-end replacement execution, API/controller integration,
changed-topology migration and bounded recovery failure handling still require
acceptance evidence. This primitive does not prove full reload completion.

Runtime replacement acceptance now covers real coordinator/worker execution:
source offset 1 is restored, old source teardown precedes replacement, output
changes from v1:first to v2:second, and the job ID stays unchanged. A failing new
map factory rolls back and emits v1:second with an explicit recovery budget.
A separate no-restart case ends FAILED with the original graph restored and no
additional output. Rollback does not override configured recovery limits.
Transactional external commit behavior and controller-driven migration still
need dedicated acceptance coverage.

The replacement runtime matrix now also uses the fenced transactional test
sink. It waits for the savepoint's first external commit before replacing,
then checks exactly two visible records after success/rollback, stable job/task
transaction namespace and increased writer generation. With no restart budget,
the external ledger keeps only the first committed record. The original matrix covers ordinary commit responses; lost-response coverage
is recorded below. Process-crash timing remains unverified.

The same-layout replacement operation is now exposed through operator-authorized
HTTP and SDK ReplaceFromSavepoint. The real-worker matrix uses that SDK/HTTP path
for all ordinary/transactional success, rollback and no-restart cases. HTTP 202
is acceptance only. Strict request tests reject malformed input/name changes
without changing the running job, and HTTPS role acceptance includes the route.
Automatic savepoint selection/orchestration and changed-topology migration remain
unfinished.

## Same-layout reload orchestration

The SDK Reload operation now sequences preflight, savepoint creation/polling,
replacement and deployment outcome polling. The six real-worker ordinary and
transactional success/rollback/no-restart cases run through this entire HTTP
sequence. WatchLiveUpdates can opt in via AllowReplacement and advances its
private baseline only on success; errors preserve the savepoint ID through
OnReload. Savepoints remain retained. Changed-topology migration, concurrent
external-edit reconciliation, process-crash acceptance and broader HTTP response ambiguity remain unfinished. TestYAMLFileReplacementThroughWorkers now covers the file-watcher opt-in with
real workers and a fenced transactional sink. An invalid edit leaves status,
deployment generation and source lifetime unchanged. A subsequent atomic CEL
edit creates a savepoint, restores source offset 1, joins the old source and
commits exactly v1:first followed by v2:second. Cancellation joins the watcher.

## CLI watch integration

`jobs watch JOB_ID --file pipeline.yaml` attaches to an existing job using the
current file as its baseline. It validates endpoint/security/arguments, emits
JSON confirmation events, and opts into same-layout reload with
--allow-replacement. CLI race tests cover atomic interval edits through HTTP and
cancellation. The SDK file-to-worker test covers replacement orchestration.
A built-binary watch smoke test, external-edit reconciliation and remaining
migration/crash requirements are still pending.

Built CLI watch acceptance is reproducible with
`python3 scripts/pipeline-watch-smoke.py /absolute/path/to/wire` after building
`./cmd`. It uses a bounded fake coordinator and checks command routing, initial
confirmation, invalid-edit isolation, exact interval PUT payload, confirmation
and clean SIGINT exit without extra mutation. It exposed and fixed SIGINT being
reported as a command failure. This does not replace the real-worker YAML reload
test or prove full multi-process coordinator/worker crash recovery.

Live interval watching now sends an expected-interval precondition. Coordinator
race/HTTP tests prove a stale writer receives a conflict and cannot alter memory
or persisted metadata; watcher tests check the baseline advances across edits
and reverts. This addresses concurrent interval overwrites, not full graph
revision conflicts or ABA detection; migration concurrency remains open.

## Lost transactional commit replies during reload

`TestSameJobReplacementLostCommitResponse` injects a successful external commit
whose first response is an error. It covers the reload savepoint in success,
rollback and no-restart cases, plus the final checkpoint after successful
replacement or recovery to the old graph. The test verifies the injection was
consumed and the external ledger contains each expected record exactly once;
the no-restart case retains only the first committed record. Source offsets,
job identity and writer generation retain the existing runtime assertions.
The original six cases and these five cases pass three times under `-race`.
This exercises a retryable commit reply loss, not coordinator/worker process
crashes or a lost HTTP response to savepoint/replacement requests.

## Lost replacement HTTP replies

Reload now sends a unique replacement request ID. The coordinator persists it
atomically with the accepted replacement and exposes it on job detail, retaining
it through rollback. The SDK sends the mutation once, then uses this ID to
reconcile a missing HTTP reply before interpreting job status. Missing or
conflicting IDs fail rather than treating the old RUNNING job as success.
The result and CLI reload event expose the ID for later reconciliation.

Coordinator tests verify durable identity before/after rollback. HTTP tests
cover accepted, rolled-back, unaccepted and conflicting requests with one
mutation each. Three real-worker transactional cases drop the accepted reply
at a reverse proxy and still verify successful replacement, rollback and
no-restart behavior, source offsets and exact external output. Each passed
three race-detected runs. This is not idempotent mutation replay, an external
configuration lock, process-crash evidence or savepoint POST reply recovery.
