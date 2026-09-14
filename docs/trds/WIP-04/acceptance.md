# WIP-04 completion audit

The following evidence covers the WIP-04 implementation. PR CI remains a
publication gate, separate from the local validation recorded here.

| Requirement | Current evidence / remaining work |
| --- | --- |
| Three strategies | Engine strategy primitives exist. RPC configuration, SDK constructors and worker resolution are added with targeted tests. |
| Ingestion timestamps | Source reader now replaces producer event time using the ingestion clock; deterministic regression test passes. |
| SDK and submission validation | Source SetWatermarkStrategy, emit interval and idle timeout survive graph conversion. Invalid strategies, durations and non-source placement are rejected before job reservation. |
| Periodic emission, 200ms default | Source emission now enters ordered chain processing. TestSourceWatermarkCannotOvertakeBlockedBatch verifies batch ordering; TestSourceWatermarkFailureCancelsIdleReader verifies failure stops idle intake (both pass with -race). |
| Multi-input minimum | Ordered input boundaries update the tracker after preceding records. Pending read-ahead and alignment records prevent idle exclusion until processing completes; TestAlignedRecordsPreventIdleWatermarkAdvance covers alignment and subsequent idle expiry. Full engine race suite passes. Embedded periodic idle exclusion is covered by TestPartitionRouterIdleInputAdvancesWithoutNewWatermark; cluster source-specific timeouts are covered by the idle-input case. |
| Idle inputs, one-minute default | Startup timeout, reactivation, all-idle and disabled-detection unit tests exist. Physical task descriptors carry source-specific idle timeouts into the receiving tracker. The three-worker idle-input test exercises this path. Subsequent stages track their own input activity with the runtime default. |
| Embedded parity | Embedded execution now resolves configured strategies and emits ordered boundaries; shuffle forwarding and aggregate window execution have passing race tests. Embedded routers now periodically reevaluate idle exclusion even without new watermark messages, preserve record ordering under downstream backpressure, and receive source timeout/interval settings. TestPartitionRouterIdleInputAdvancesWithoutNewWatermark and the full SDK race suite pass. Subsequent stages use the runtime default for their own input activity, matching worker execution. |
| Window closure | EventTimeWindowOperator and ordered chain callbacks now produce downstream results before forwarding watermarks. Registered worker windows and embedded aggregate windows execute in tests. |
| Late data | The bounded-ooo cluster case processes reordered timestamps 8,2,30 correctly. The late-record case waits for the first window result, then sends timestamp 2 again and advances with 50; the next result contains only the on-time record at 30. All cases pass with -race. The operator also exposes a Late callback, covered by its unit test. |
| Recovery | Source checkpoint handshake now freezes periodic emission from offset capture through chain acknowledgement; TestSourceCheckpointFreezesPeriodicWatermarks passes with -race. The window operator restore test prevents refiring a completed window. The cluster restore case now completes a replicated checkpoint, injects a source failure, verifies restoration before the next source read, and advances a watermark to close the restored pending window. Total results remain three records, including the two already emitted before checkpoint; no completed window refires. It passes with -race. The restore case now advances past the completed checkpoint before failure: the next window fires uncommitted, is replayed after restoration, and is committed only by a subsequent checkpoint. Four attempted output records yield exactly three committed records (the replayed window contributes once). This extended case passes with -race. |
| Cluster tests | TestClusterWatermarkStrategiesCloseWindows passes with -race for all three strategies, late-record rejection, and a three-worker idle-input case with a 50ms source-specific timeout. The full worker and SDK race suites also pass. The restore case also passes with -race using actual checkpoint replication and worker restart. Post-checkpoint advancement, failure, replay, and transactional visibility are covered by the extended restore case. |
| Configuration/docs/PR | The strict wire/v1 Pipeline parser now accepts spec.sources[].watermark, validates before connector factories, and preserves settings in graph conversion. TestYAMLWatermarkConfiguration and existing YAML tests pass with -race; the proposal example now uses the actual document envelope; document defaults and supported API. Full tests and lint pass; the linked personal-account follow-up PR is the remaining publication gate. |

## Ordering invariant

For a watermark W, every record already observed to generate W must pass through
the relevant operators before W. The former direct-to-output source emitter
violated this when the event channel or operator was slow. Moving the emitter
alone to a priority control channel would also be unsafe: the chain drains
control messages before records. Ordered boundaries must cover source intake,
network input queues, local operator callbacks and downstream output.

The current checkpoint source-boundary handshake already serializes snapshot
capture with drained batches; watermark integration must preserve that behavior.
Watermark state is ephemeral per the WIP, while source offsets and window state
must restore consistently at the checkpoint boundary.

## Default strategy decision

Unconfigured worker and embedded sources use bounded out-of-orderness with a
five-second tolerance, resolving the proposal's default-tolerance question using
the existing DefaultMaxOOO constant. Legacy GenerateWatermark methods remain in
the source interfaces for compile-time compatibility but no longer select runtime
watermarks. Sources needing strict timestamp ordering should configure
MonotonicTimestamps explicitly. TestUnconfiguredSourceUsesBoundedWatermarks and
TestEmbeddedDefaultWatermarkIsBounded verify the default formula.

Explicit `max_ooo: 0s` and `BoundedOutOfOrderness(0)` retain zero tolerance across configuration boundaries. An omitted tolerance remains five seconds; RPC uses an optional duration to preserve this distinction.

## Signed timestamp audit

Generation, per-input tracking, and last-emitted boundaries initialize at
MinInt64. The strategy preserves `maxObserved - maxOOO`, saturating only at the
integer limit. TestNegativeWatermarkClosesWindow verifies a pre-epoch window
through ordered source emission; full engine and SDK race suites pass.

## Delivery gates

`go test ./...` passed on the final implementation. Engine and SDK race suites,
worker strategy/idle/late/recovery cases, and explicit-zero regression tests
passed. `golangci-lint run ./...` reported zero issues. Changed Go files were
formatted with goimports. The follow-up PR links the original WIP-04 PR #205;
its remote CI must pass before this task is closed.


## Embedded routing review

The router uses per-destination send locks for records and watermark boundaries.
A record waiting for capacity does not hold the global watermark broadcast lock,
and pending records stay active for idle detection. The regression test holds one
partition full beyond its idle timeout while another input sends records and a
watermark. See [compatibility notes](../../wip-04-release-notes.md) for the default
strategy change, periodic watermark traffic, and remaining follow-ups.
