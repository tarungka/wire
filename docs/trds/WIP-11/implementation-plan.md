# WIP-11 completion plan — 2026-09-22

Branch: `codex/wip-11-complete`, based on master `b76fa56`. WIP-10 #224 remains separate. Follow-up to #194; implementation requirements are verified in the [acceptance record](acceptance.md).

## Requirements and acceptance

1. Correct transient/poison/fatal classification and fixed/exponential/none retry behavior, including cancellation, bounded delays, reclassification, panic handling, and preservation of error causes. Failed Map/FlatMap/Filter/Process attempts must not leak partial output to the main stream. Preserve original payload in the DLQ envelope.
2. Strict YAML `error_handling` parsing with the proposal's duration fields and actions, validated before connector factories run. Reserved `__dlq__` input binds one configured side-output sink to policies selecting DLQ; it is not a normal data edge. Reject ambiguous, cyclic, unsupported or recursive configurations clearly.
3. Preserve per-operator policies through SDK graph conversion and worker deployment. Exercise real MiniCluster paths for all five named scenarios: 99 normal/1 DLQ, transient sink retry, permanent sink failure, panic-to-DLQ continuation, and missing-DLQ drop/log.
4. Export live operator error, retry, DLQ and drop counters from embedded and distributed runtimes, attributed by operator and error class where required. Preserve explicit noop injection for tests/opt-out; verify actual metric collection, not only mocked increments.
5. Preserve best-effort DLQ semantics: failed/full/missing destinations log and count drops; successful delivery counts once. Verify sink Open/Close ownership and isolation from normal checkpoint transactions. Document that best-effort DLQ can lose records and can duplicate on replay.
6. Verify unit coverage target (100% for retry/classification/backoff logic), failed DLQ delivery and retry/checkpoint interaction. Validate security: full original payload is deliberate; no stack dump in the error envelope.
7. Update current usage, WIP status, acceptance evidence and index only after verification. Run full race/integration tests, build, vet, lint; publish a linked follow-up PR using personal account tarungka and verify CI. Do not alter workflow branch filters.

## Completion audit

Requirements 1–6 have direct unit, MiniCluster, runtime-metric and cross-worker
transport evidence in [acceptance.md](acceptance.md). The full repository race
suite, build, vet and pinned lint checks pass. Requirement 7's local documentation
and checks are complete; publication and GitHub CI are tracked on the follow-up
PR. Local test results are not represented as GitHub CI results.

The audit also resolved gaps that passing earlier tests had missed: partial
output escaping failed calls, mutation of retry/DLQ input bytes, cancellation
being swallowed by exhausted policies, a log-only queue counting missing DLQ
records as delivered, and DLQ lifecycle failures escaping into the main job.

The existing synchronous Sink.Write contract is preserved. The production HTTP
sink delegates to WriteBatch, and its actual retry/failure acceptance test
disables connector retries to isolate the operator policy. This is not evidence
of automatic batching, which this WIP does not add.
