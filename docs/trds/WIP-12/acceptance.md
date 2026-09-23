# WIP-12 acceptance evidence

Implementation branch: `codex/wip-12-complete`, based on master `931d02e`.
[Completion PR #227](https://github.com/tarungka/wire/pull/227) completes the runtime scope left by [#193](https://github.com/tarungka/wire/pull/193).
See the [runtime contract](runtime-contract.md) for configuration, storage bounds,
side-output delivery and upgrade requirements.

## Required scenarios

| Requirement | Executed evidence |
| --- | --- |
| On-time arrival, initial firing, allowed late update | `TestMiniClusterLateOutputAndUpdatesAllWindows`: nine combinations of tumbling/sliding/session and Aggregate/Reduce/Apply, with source progress gated on observed downstream results |
| Expired event sent once to named late output | Same nine MiniCluster cases check original payload/headers, no result metadata, no delivery to main sink; `TestClusterWindowLateOutputCheckpointRecovery` checks the actual distributed path for all three window types |
| Zero lateness, default drop and live metric | `TestWindowRuntimeMetricsRestorePurgeAndDefaultDrop`: all three window types with zero and nonzero lateness, no late destination, exported counter assertions rather than only local stats |
| Purge at retention deadline deletes Pebble state | `TestWindowAllowedLatenessAndPurgeBoundary` checks immediately before/at the boundary; `TestWindowPebblePurgeReopenAndPortableRestore` checks backend records, reopen, deletion and portable restoration for all three kinds |
| Checkpoint before late arrival, restore and replay | `TestClusterWindowLateOutputCheckpointRecovery`: real coordinator, two workers, replica archive transfer, failure/redeploy, update count/identity and late branch; no unchanged initial result refires; a subsequent checkpoint completes across both branches |

MiniCluster is the repository's embedded harness. It is not evidence of network
checkpoint replication by itself. The distributed test uses ordinary sinks and
explicitly expects replay across attempts; it does not claim transactional
external visibility. The recovery source remains live between scripted batches,
so this test does not exercise or claim to fix WIP-10's known mixed-source final
checkpoint limitation.

## Configuration, storage and routing

| Requirement | Evidence |
| --- | --- |
| SDK duration units, legacy integer milliseconds, negative/sub-ms rejection | `TestAllowedLatenessUnitsAndValidation` |
| YAML zero/30s/1h and invalid lateness on every window kind | `TestYAMLAllowedLateness`; `TestYAMLWindowLateOutputExecution` executes a named branch through the YAML pipeline |
| Cluster SDK factory and serialized dimensions | `TestNamedWindowDeploymentRoundTrip`, `TestConfigureWindowPreservesFactoryAndRejectsUsedState`; distributed recovery deliberately uses different factory defaults to prove deployment dimensions take effect |
| Reject invalid definitions before scheduling/factories | `TestWindowGraphValidationBeforeScheduling`, `TestWindowDeploymentValidationBeforeFactory`; also rejects manually supplied record error policies on windows |
| Result bounds/update identity survives transport | `TestWindowResultMetadataPreservesUpdatesAcrossTransport`, `TestWindowResultRejectsMalformedMetadata`; SDK Reduce/Apply tests verify payloads and WindowInfo.IsUpdate |
| Tagged physical output groups and invalid tag rejection | `TestPhysicalWindowLateOutputGroups` |
| Main/late data separation; barrier, watermark and end fences to both | `TestGroupedRouterSeparatesLateDataAndFencesBothOutputs` over real transport streams; distributed checkpoint completion confirms both branches acknowledge |
| Configured backend lifecycle and portable state | `TestWindowOperatorOpensConfiguredBackend`, `TestWindowPebblePurgeReopenAndPortableRestore` |
| Failed state publication cannot advance state | `TestWindowBackendFailureDoesNotAdvanceState`; orphaned records without metadata rejected by `TestWindowBackendRejectsOrphanedRecords` |
| Logical state/window-count limits | `TestWindowPayloadLimitRejectsAtomicGrowth`, `TestWindowStateLimitAndTimestampOverflow`, `TestWindowTimestampExtremesAndLimits` |
| Attributed metrics, purge, restore, no historical counter duplication, unregister | `TestWindowRuntimeMetricsRestorePurgeAndDefaultDrop`, `TestWindowMetricsAttributionRetentionAndClose` |

## Semantics and edge cases

- `TestWindowSlidingPartialLatenessAndNegativeTime` covers a record accepted by
  surviving overlapping windows and routed only when all have expired.
- `TestWindowSessionMergeAfterEmission` covers merging a previously emitted
  session and its updated identity without retracting prior outputs.
- `TestWindowHundredLateUpdates` covers 100 repeated updates for every window
  kind, exact count, bounded retained-window count and complete purge.
- `TestWindowTimestampExtremesAndLimits` covers overflow/underflow, saturated
  retention deadlines and invalid limits.
- `TestWindowCheckedFailuresPreserveSnapshot` proves add/merge/result failure
  atomicity, watermark failure propagation and retry against unchanged state.
- `TestWindowSnapshotRestoresProgressAndRejectsCorruption` checks corrupt snapshot
  rejection without modifying live state and no resurrection after purge.
- `TestWindowReduceAndApplyPreserveLateUpdates` checks SDK functions, bounds,
  update identity and checkpoint replay for all three kinds.

The unit command `go test ./internal/engine -run Window -coverprofile=window.cover`
and `go tool cover -func=window.cover` report **100% statement coverage for every
function in window_processor.go and window_aggregator.go**. That is the WIP's
late-detection/retention/purge semantics target. It is not 100% coverage of the
engine package, storage adapters or snapshot serialization. Callback failure
and storage/recovery behavior have additional tests listed above.

## Verification gates

- Final pre-publication `go test -race -timeout 5m ./...` passed, including
  the worker integration suite (84.7 seconds).
- `go build ./...` and `go vet ./...` passed.
- CI-pinned `golangci-lint v2.5.0 run --timeout=5m` passed with zero issues.
- GitHub CI is authoritative for remote checks; these local results do not
  imply remote CI has run or passed.

Automatic lateness tuning, per-key lateness, retractions and exactly-once
ordinary-sink output remain outside this WIP's scope. Apply's retained input
records and the in-memory working cache remain subject to documented bounds.
