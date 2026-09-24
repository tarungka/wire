# WIP-18 completion audit

This follow-up retains the original WIP-18 scope. It is not yet complete. It is
stacked on WIP-17 so the SDK/worker/checkpoint integrations from WIP-13–17 remain
available. The historical proposal's pseudocode and implementation-status notes
are not evidence that every requirement is implemented.

| Requirement | Evidence or remaining work |
| --- | --- |
| Two interchangeable backend implementations | Engine factory, HashMap and Pebble implementations and shared contract tests exist. Audit against every specified method and failure case remains. |
| HashMap ordered storage | Implemented with `github.com/tidwall/btree` v1.8.1. Ordered prefix iteration snapshots owned bytes; atomic batches use copy-on-write. Legacy snapshots remain readable; new snapshots now carry the required magic header. Regression tests cover 10,000 reverse-order inserts, snapshot bytes, stable prefix iteration, rejected unordered/duplicate snapshots and atomic memory-limit rejection. |
| SDK backend selection and worker execution | Existing graph specs, worker factory injection, scoped state and distributed tests exist. Full original acceptance audit remains. |
| MiniCluster defaults to HashMap, Pebble remains overridable | This follow-up selects HashMap with a 256 MiB logical payload limit. A real-worker test inspects checkpoint backend identity during Process execution and checks keyed-state continuity for both the default and an explicit Pebble override. |
| MiniCluster startup below 100 ms | Lifecycle benchmark measures construction through the first actual stateful invocation, plus total completion/shutdown separately. Record local measurements; do not use constructor-only timing or promise this target for every host. |
| Node configuration, CLI and environment defaults | Node state settings, both CLI flags and environment names now resolve omitted managed-operator choices at submission and persist them. Tests cover node precedence, explicit SDK choices, validation and recovery stability. Full pipeline precedence remains below. |
| Pipeline YAML and full selection precedence | Preserve the original pipeline field and SDK/CLI/pipeline/system/default ordering. Integration with WIP-19 remains required; not removed from scope. |
| Memory limits and safeguards | Existing logical payload accounting and errors need full boundary/overflow/restore audit. Worker aggregate admission against available memory remains open. Runtime overhead and snapshot/iterator copies must be documented accurately. |
| Memory observability | The proposal names `wire_state_backend_memory_bytes` with backend/task attribution. No matching runtime metric exists yet; implement and verify it before completion. |
| Checkpoint format and metadata | HashMap writes `WHSB`, version 1, length-prefixed entries and CRC32, and reads legacy unframed version-1 snapshots. Fixed byte fixtures verify upgrade compatibility and malformed-header rejection. Backend-tagged handles exist; backend mismatch, native Pebble semantics and durable manifest evidence remain in the final audit. |
| Replication, restore and retention | Current worker archive transport and retention code exist from earlier WIPs. Prove both backends through actual completed-checkpoint recovery and cleanup; helper round trips alone are insufficient. |
| Rescaling | Both backends pass real coordinator/two-worker savepoint rescale tests for 4→8, 8→4 and 4→3, including replicated fetch, assigned-key validation, replacement checkpoint and old-savepoint release. Backend restore rejects gaps, overlaps, mixed checkpoints, corruption and cancellation atomically. SDK managed Process now implements typed key-group restore, with shared HashMap/Pebble state/TTL/timer tests at the same sizes. Managed window redistribution now has operator-level parity tests for both backends, all three window kinds and all required sizes. MiniCluster managed Process rescale now passes for both backends at all three sizes, including restored state, a replacement savepoint and deletion of the old savepoint. MiniCluster window rescale now also passes for both backends, all three window kinds and all three sizes. MiniCluster stopped-worker recovery passes for both managed backends with surviving replicas and checkpointed source offsets. Replica-loss cases remain part of the final durability audit. |
| Contract/negative tests | Shared `TestStateBackendAcceptance` verifies every entry of a 10,000-entry restore, empty restore, 10 MiB value, binary key groups 0x0000–0x007F with ordered 0x0020 prefix selection, and checkpoint consistency during concurrent atomic updates/Get. Three runs pass under `-race` for both backends. Existing corruption, cross-backend rejection and memory-limit cases still need final requirement mapping. |
| Comparative benchmarks | Implemented reproducible Put/Get/full-iterator and 1/64/256 MiB checkpoint benchmarks for both backends. [Local measurements and raw output](benchmarks.md) distinguish volatile writes from synchronized writes and serialization from native checkpoint hashing; proposal estimates are not guarantees. |
| Documentation and upgrade behavior | Record current formats, defaults, resource boundaries and incompatibilities, link runtime guidance, then audit all original sections before marking Implemented. |

## MiniCluster backend default

`GetExecutionEnvironment` now installs `NewHashMapStateBackend(256)` before
returning the environment. A later `SetStateBackend` call overrides it normally.
This changes managed Process/window instance state only. Coordinator metadata
and checkpoint replica stores still use their existing storage; MiniCluster is
not disk-free. Ordinary `sdk.New()` and production defaults remain unchanged.
The logical payload limit excludes Go runtime overhead, iterator copies and
checkpoint buffers and is per managed operator instance, not a worker RSS cap.

Run the evidence:

```sh
go test -race ./sdk -run TestMiniClusterDefaultBackendAndPebbleOverride
go test ./sdk -run '^$' -bench '^BenchmarkMiniClusterStateBackendLifecycle$' -benchtime=3x
```

The benchmark includes actual local coordinator/worker creation, registration,
placement, transport setup and a stateful record invocation. `startup-ns/op`
measures up to that invocation; normal `ns/op` additionally includes final
checkpoint completion and shutdown. Debug logging is disabled during the
benchmark and restored afterwards. Performance samples are local observations,
not CI pass/fail timing assertions.

Initial local sample (Apple M4, darwin/arm64, Go benchmark with three iterations,
not race-instrumented):

| Backend | Startup to stateful invocation | Complete lifecycle |
| --- | --- | --- |
| HashMap | 27.0 ms/op | 77.1 ms/op |
| Pebble | 64.8 ms/op | 2102.8 ms/op |

The HashMap sample meets the proposal's local startup target. Three observations
are not a percentile or cross-platform performance guarantee. The lifecycle
measurement includes checkpoint/scheduler/shutdown costs and must not be quoted
as backend Put or checkpoint serialization latency.

## Persisted node defaults

The [selection guide](../../state-backend-selection.md) documents node state
configuration and runtime boundaries. `SubmitJob` and savepoint submission share
default resolution before publication. Explicit operator specifications are
preserved; recovered jobs retain their stored specification even when the new
coordinator has another default. Managed savepoint upgrades compare backend
formats and reject migration before deployment while permitting a different
worker-local Pebble root. The system default is Pebble; node-selected HashMap
uses 256 MiB unless explicitly overridden, including zero for unlimited.

Config/command/coordinator race suites pass. Targeted tests exercise actual CLI
parsing, file→environment→explicit-flag precedence, alias conflicts, overflow,
immutable caller bytes, no persistence on invalid selection and recovered graph
stability. The complete YAML/CLI/SDK precedence and aggregate worker memory
admission are still required before marking this WIP implemented.

## B-tree index validation

The full engine and SDK suites pass with `-race` after replacing the sorted
slice. Existing unframed version-1 snapshots remain readable; restore rejects
unordered or duplicate keys instead of silently changing cardinality or memory
accounting. The memory cap still measures logical key/value payload, not tree
allocation overhead or process RSS. Comparative throughput and checkpoint measurements are recorded in
[the benchmark baseline](benchmarks.md).

## Shared backend acceptance cases

Run `go test -race ./internal/engine -run '^TestStateBackendAcceptance$' -count=3`.
The test uses the same cases for HashMap and Pebble. Roundtrip checks read all
10,000 records from a separate restored backend. The concurrent case races
checkpoint creation with paired atomic updates and reads, then restores each
snapshot into a separate instance and checks that its pair is consistent.
This is local backend acceptance; it does not substitute for distributed
replication, task-loss recovery, retention or key-group rescale acceptance.

## Snapshot magic and compatibility

The serializer now emits the proposed `WHSB` header. `TestHashMapSnapshotFormatUpgrade`
uses fixed old/new byte fixtures and proves that old snapshots restore and are
re-emitted in the current format. Header tests recompute valid CRCs for invalid
magic, unsupported versions, truncated headers and oversized lengths/counts,
so structural validation is exercised independently of checksum rejection.
Malformed restores leave existing state unchanged. See the
[selection guide](../../state-backend-selection.md#snapshot-format-upgrades)
for the worker upgrade and downgrade boundary.

After the magic-header change, the full engine, worker and SDK suites pass
with `-race`; engine lint reports zero issues.

## Distributed rescale and atomic range restore

HashMap now implements `VisitKeyGroupRange` and `RestoreKeyGroupRanges`.
Replacement state is assembled privately with the destination logical memory
limit; only a fully validated result is published. The two backends share
validation of contiguous, non-overlapping ownership and checkpoint identity.

`TestClusterSavepointRescalesKeyGroups` runs both backends on a real coordinator
and two workers with replica servers, through the HTTP rescale API. It checks
4→8, 8→4 and 4→3, plus unsupported-source rollback and a delayed state fetch.
Restored values and ownership are checked before operator Open can initialize
state. A new checkpoint must complete before deleting the old savepoint.
The cluster test and both backends' range-restore tests pass under `-race`;
engine/worker lint is clean. This test uses a registered stateful test operator,
not SDK MiniCluster managed Process; the latter remains a separate integration
requirement, as does recovery from actual worker loss.

## SDK managed Process rescale

The Process adapter now implements typed `RestoreKeyGroupState`. It understands
the existing SDK state-key layout rather than treating its first two bytes as
a key-group prefix. The worker supplies the job's immutable key-group count.
Values, lists, maps, TTL entries and timers follow their embedded user key using
the same hash function as routing. The restored operator-wide watermark is the
minimum across contributing snapshots, with a missing watermark treated as the
initial minimum. This preserves existing saved-state encoding.

`TestManagedProcessRescaleState` checks 4→8, 8→4 and 4→3 for both backends,
including wrong-owner absence, complete timer coverage without duplicates,
TTL expiry after restore and watermark selection. Malformed state keys and
watermarks fail without replacing existing destination state. These are real
operator/backend tests; they do not claim MiniCluster HTTP-rescale coverage.
Snapshot assembly uses temporary backend storage and scans contributing
snapshots; optimizing this to a prefixed layout would require a separate
state-format migration. The destination backend still enforces its memory limit
on publication; temporary restore memory/disk is additional resource use.

Full SDK and worker suites pass under `-race` after this integration; lint of
SDK and worker packages reports zero issues.

## Typed window checkpoint path

Event-time windows now expose the selected backend's typed snapshot handle to
checkpoint replication. Portable `Checkpoint`/`RestoreCheckpoint` remain for
legacy processor snapshots. Typed restore first validates a private backend and
reconstructs a candidate WindowProcessor, then replaces the live backend and
cached state only after validation succeeds. Backend memory-limit checks still
apply during publication.

`TestWindowTypedSnapshotBackends` covers tumbling, sliding and session windows
with both backend types. It checks accumulator continuity, no repeated firing
at a restored watermark, and atomic rejection of a checksummed backend snapshot
whose window metadata is malformed. This supplies the typed checkpoint prerequisite; window redistribution and
differing partition progress are covered by the subsequent implementation below.

The full engine, worker and SDK suites pass under `-race` with typed window
checkpoints enabled; engine lint reports zero issues.

## Window redistribution and event-time progress

`EventTimeWindowOperator.RestoreKeyGroupState` now selects retained windows by
user-key hash and rebuilds them privately before publishing typed state. The
worker supplies the fixed job key-group count. Coverage/identity checks reject
gaps, overlaps and mixed checkpoints; the existing window configuration and
state-size limits still apply. Source operational late/drop counters reset for
the new task because they cannot be apportioned among key groups; retained
window counts and state bytes are rebuilt from selected state.

A rescaled window stores per-key-group watermark floors. Processing, late-data
classification, firing, purge and retention accounting use the greater of the
current input watermark and the key group's restored progress. This preserves
already-purged history even when another contributing partition was behind.
Floors also survive ordinary typed checkpoint/restore and subsequent rescaling.
Snapshots carrying floors use window snapshot version 2; readers without this
feature reject them rather than silently dropping progress. Legacy version 1
remains readable. Backend format versions are independent of this window format.

`TestWindowRescaleMatchesOriginalPartitions` checks HashMap and Pebble, tumbling,
sliding and session windows, and 4→8, 8→4 and 4→3. Source watermarks deliberately
differ (5, 12 and 20), spanning unfired, fired-retained and purged windows. After
rescaling and another checkpoint/restore, late-record results and final watermark
outputs must match the original partitions. Invalid version/hash-space/progress
metadata is rejected without altering live windows. These are operator tests;
full MiniCluster rescale and worker-loss recovery acceptance are still separate.

With window redistribution enabled, the full engine, worker and SDK suites
pass under `-race`; engine lint reports zero issues.

## MiniCluster managed Process acceptance

MiniCluster exposes a detached list of active `MiniClusterJob` entries through
`Jobs()`. Each entry identifies the job and its loopback HTTP coordinator API.
Endpoints are removed on execution teardown. `NumWorkers` sets a minimum worker
count so tests can reserve capacity for a later scale-up; zero retains automatic
initial sizing. These APIs are for local integration testing, not deployment.

`TestMiniClusterManagedProcessRescale` starts an SDK graph on real MiniCluster
workers and replicas, waits for 32 distinct keys to reach the sink, takes a
savepoint through HTTP, rescales Process and sink, and sends the same keys again.
Each key must produce exactly count 1 then count 2. It then completes a replacement
savepoint and deletes the old one. Both backends pass 4→8, 8→4 and 4→3 under
`-race`. Sources stay at one instance and supply explicit test data between
boundaries; this proves state redistribution, not external source replay.

A post-control-API lifecycle sample (three iterations, Apple M4, alongside the
SDK race suite) measured HashMap startup at 46.1 ms and Pebble at 103.3 ms.
HashMap still met the local <100 ms target in that sample. Concurrent test load
makes this unsuitable for comparison with the earlier standalone measurements;
these observations remain workload/host-specific, not an SLA.

The complete SDK suite passes with `-race`, including endpoint cleanup checks;
SDK lint reports zero issues.

## MiniCluster window acceptance

`TestMiniClusterWindowRescale` runs HashMap and Pebble, tumbling/sliding/session
windows, and 4→8, 8→4 and 4→3 through the real MiniCluster HTTP control, scheduler,
worker data streams and checkpoint replicas. It observes the first 32 keys being
added to window accumulators, saves them before firing, rescales, then adds the
same keys again. A subsequent source timestamp advances the watermark. Every
result must have count 2, with exactly the expected windows per key. The test
also completes a replacement savepoint and deletes the original one.

The full new matrix and existing MiniCluster Process rescale tests pass together
under `-race`; SDK lint is clean. The source is deliberately controlled between
boundaries, so this establishes window-state redistribution and completed
checkpoint retention behavior, not real external-source replay or worker loss.

## Recovery after an in-process worker stops

`MiniCluster.StopWorker(ctx, jobID, workerID)` stops that worker's execution,
data connections and replica services while leaving its coordinator and peers
running. Worker Run errors remain fatal unless its own child context was
explicitly cancelled. The method is idempotent for a still-listed stopped worker,
waits for its runtime teardown (or caller cancellation), and does not kill an OS
process. Execution teardown removes the worker-control entries.

`TestMiniClusterBackendRecoveryAfterWorkerLoss` starts three workers, checkpoints
32 keyed records and their source offset, stops a worker hosting managed state,
then waits for replacement tasks on survivors. The new source must restore its
saved offset before the second batch is released. Every key must emit count 1
then count 2; a replacement savepoint must complete and release the original.
HashMap and Pebble both pass under `-race`.

The chosen worker's task replicas survive on other workers. The current single
replica placement policy chooses the first eligible peer; this test deliberately
does not kill a worker holding another task's only archive. It therefore does
not claim tolerance of losing the last replica, arbitrary simultaneous worker
loss, abrupt OS termination or a host/disk failure. Those boundaries must remain
explicit in the final durability audit.

The full SDK suite passes with `-race` after worker-stop integration, including
the pre-existing offset recovery and shutdown tests; SDK lint is clean.
