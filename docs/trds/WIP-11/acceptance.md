# WIP-11 acceptance evidence

Implementation branch: `codex/wip-11-complete`, based on master `b76fa56`.
[Completion PR #225](https://github.com/tarungka/wire/pull/225), following [#194](https://github.com/tarungka/wire/pull/194). WIP-10 #224 is
separate and is not included in this branch.

## Requirement mapping

| Requirement | Evidence |
| --- | --- |
| Transient / poison / fatal classification and fixed / exponential / none retries | `internal/engine/error_handler_test.go`, `error_policy_boundaries_test.go`: standard network/resource errors, explicit markers, custom classifier, fatal precedence, invalid classification, delay caps, cancellation and reclassification |
| Failed attempts cannot leak partial output; original payload survives retries and DLQ | `TestErrorPolicyDiscardsPartialOutput`, `TestRetryAndDLQPreserveOriginalPayload` cover Map, FlatMap, Sink and panic paths |
| All exhausted actions | `TestMiniClusterErrorPolicyRouting` exercises fail/drop/DLQ, continued processing after panic and missing destination |
| 1 poison record out of 100 | `TestMiniClusterErrorPolicyRouting`, `TestYAMLDLQSharedDestination`: 99 distinct normal records, one original DLQ record |
| Transient Sink WriteBatch failure then success, no loss | `TestMiniClusterBatchSinkRetries/exhausted=false`: production HTTP Sink.Write delegates to WriteBatch; connector retries disabled; two operator retries, three records accepted once each |
| Permanent failing sink exhausts retries and fails | `TestMiniClusterBatchSinkRetries/exhausted=true`: persistent 503, three calls, no accepted output, ErrRetriesExhausted and original ErrTransient remain discoverable |
| Filter / FlatMap / Process policies | `TestMiniClusterTransformationErrorPolicies` exercises SDK adapters, including a keyed Process stage and partial output returned with errors |
| Serialized policies and named DLQ across worker transport | `TestErrorPolicyAcrossWorkerStreams`: descriptor msgpack round trip, separate producer/consumer muxes, 99 main records / one named DLQ record, lifecycle and routing cleanup |
| Strict YAML policy and reserved input | `pipeline_error_policy_test.go`: validation before factories; invalid durations/actions/unknown fields, ambiguous and recursive DLQs rejected; one destination opens/closes once for multiple operators |
| Missing/full/failed DLQ behavior | `TestMissingDLQLogsAndCountsDrop`, `TestTaskSlotMissingDLQCountsDrop`, existing full-channel/writer failure tests; missing destinations log ERROR and count drops, never successful delivery |
| Best-effort lifecycle isolation and cancellation | `TestDLQLifecycleFailureIsolation`, `TestMiniClusterDLQLifecycleFailures`, `TestCancellationDuringDLQWrite`: Open/Close errors and panics do not fail normal processing; blocked writes receive the chain context |
| Retry/checkpoint ordering | `TestRetryFinishesBeforeCheckpointBarrier`: enqueue a barrier while retry is blocked; successful record precedes the barrier |
| Live error / retry / DLQ / drop metrics | `TestOperatorErrorMetrics` collects OTel counters with bounded classes; `TestYAMLDLQSharedDestination` and `TestTaskExecutorNamedDLQ` collect actual runtime counters with operator and distributed task attribution |
| Security and reliability contract | JSON envelope retains original binary key/value/header bytes; safeInvoke adds the immediate panic error without a runtime stack; no checkpoint/transaction methods are exposed by DLQSink. Usage documents best-effort loss/replay duplication and idempotent retry obligations |

`MiniCluster` is the repository's embedded test harness. The worker-stream test
separately verifies the distributed TaskSlot and real data transport path; the
MiniCluster tests alone are not evidence of a coordinator deployment.

The engine invokes synchronous Sink.Write. Batch-capable sinks can delegate
Write to WriteBatch, as the tested HTTP connector does. WIP-11 does not introduce
asynchronous buffering or automatic multi-record batches.

## Verification

- Full engine, SDK and observability race suites passed during implementation.
- All MiniCluster error-policy scenarios, HTTP sink tests and the worker-stream
  acceptance test passed with `-race`.
- Full engine coverage profile reports **100% statement coverage** for every
  function in `error_handler.go`: fixed/exponential backoff, default/custom
  classification, panic wrapper, retry loop, exhausted handling and link creation.
  This is the proposal's core-logic target, not a claim of 100% repository coverage.
- `go test -race -timeout 5m ./...` passed for the full repository, including
  worker integration tests (77.7s).
- `go build ./...`, `go vet ./...`, and CI-pinned `golangci-lint v2.5.0 run
  --timeout=5m` passed (`0 issues`).
- GitHub CI results are tracked on the follow-up PR; the local results above
  do not claim a successful remote CI run.

Exactly-once DLQ delivery, replay tooling, circuit breakers and global error-rate
limits remain explicitly outside WIP-11's scope.
