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
