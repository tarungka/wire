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
| Checkpoint format and metadata | HashMap writes `WHSB`, version 1, length-prefixed entries and CRC32, and reads legacy unframed version-1 snapshots. Fixed byte fixtures verify upgrade compatibility and malformed-header rejection. Backend-tagged handles exist; backend mismatch, native Pebble semantics and durable manifest evidence remain in the final audit. |
| Replication, restore and retention | Current worker archive transport and retention code exist from earlier WIPs. Prove both backends through actual completed-checkpoint recovery and cleanup; helper round trips alone are insufficient. |
| Rescaling | Prove HashMap 4→8, 8→4 and 4→3 key-group redistribution through the distributed runtime, with equivalent Pebble behavior. |
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
