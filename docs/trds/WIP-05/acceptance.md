# WIP-05 acceptance evidence

Validated on 2026-09-14. This completes the timeout and failure-handling scope of WIP-05; unaligned checkpoints, partial completion, and automatic stall diagnosis remain non-goals.

| Requirement | Implementation | Regression evidence |
| --- | --- | --- |
| Abort incomplete checkpoints after timeout | Coordinator persists the abort and queues fenced task commands | `internal/coordinator/checkpoint_policy_test.go`; `internal/worker/checkpoint_timeout_cluster_test.go` |
| Release alignment data without loss or reordering | Pre-barrier queued records drain before post-barrier buffers; retired identities reject delayed barriers | `internal/engine/control_priority_regression_test.go`; `internal/engine/barrier_abort_regression_test.go` |
| Recover after a missing barrier | Two workers run two source instances feeding a sink; one source withholds its barrier, then resumes | `TestClusterAlignmentTimeout` tests in `internal/worker/checkpoint_timeout_cluster_test.go` |
| Enforce optional failure budgets | Persisted counters, consecutive limit, and positive failure-rate threshold; abort delivery precedes recovery cancellation | `internal/coordinator/checkpoint_policy_test.go`; distributed repeated-timeout test |
| Avoid resurrecting an aborted checkpoint | Aligner and source reject retired checkpoint/epoch identities; transaction aborts are idempotent | `internal/engine/barrier_abort_regression_test.go` |
| Expose alignment health | Timeout counter, alignment duration histogram, buffered logical-byte gauge | `internal/observability/checkpoint_test.go`; `internal/observability/task_test.go` |
| Validate and wire configuration | YAML/JSON config reaches coordinator and worker; minimum pause returns HTTP 409 `CHECKPOINT_MIN_PAUSE` | `internal/config/checkpoint_policy_test.go`; coordinator policy tests |

## Policy semantics

- `checkpoint.timeout` defaults to 10 minutes. The coordinator checks expiry on its existing two-second maintenance cadence; this is not a hard real-time deadline.
- `checkpoint.min_pause` defaults to zero and measures time since durable checkpoint completion. Duplicate acknowledgements do not move that timestamp.
- `checkpoint.max_consecutive_failures` defaults to zero (unlimited). A positive limit is reached when the failure count equals it. Completion resets the consecutive count.
- `checkpoint.tolerable_failure_rate` defaults to zero (disabled), preserving the engine's existing behavior. A positive value compares failed attempts with all triggered attempts. The original proposal's “zero means no tolerance” was not implemented and is explicitly superseded here.
- Timeouts and worker-reported checkpoint failures consume the budget once per checkpoint. Explicit administrative aborts do not. Threshold failures move the job to FAILING, after which the existing recovery policy decides restart versus terminal failure.
- Failure counters and the latest `checkpoint_failure` survive coordinator metadata reloads. Success clears that reason.

## Delivery and cleanup limits

A durable coordinator abort decision is distinct from a durable task-side acknowledgement. Commands are fenced and idempotent, with command-stream/heartbeat delivery; disconnect and recovery still handle tasks that shut down before applying cleanup. The proposal's abstract `AbortCheckpoint → Ack` does not imply a new durable per-task abort receipt protocol.

Aborts cancel pending checkpoint uploads and release alignment buffers. Synchronous user snapshot callbacks cannot be forcibly interrupted, so task cleanup can wait for a callback to return. Already forwarded barriers cannot be retracted; retired identities prevent them from restarting alignment. Physical snapshot artifact deletion follows existing backend retention and staging cleanup rather than deleting arbitrary user-owned files.

The buffered-byte gauge measures logical payload bytes, excluding container overhead. The existing `wire_task_alignment_buffer_bytes` gauge is retained alongside `wire_checkpoint_alignment_buffered_bytes`. Timeout counters carry `job_id`; alignment instruments carry `task_id` when available.

## Validation

- `go test -race ./...` — passed.
- `go test -race ./internal/worker -run TestClusterAlignmentTimeout -count=3` — passed; verifies release order, a successful next checkpoint without job restart, and repeated-timeout failure policy over actual worker RPC/data transport.
- `golangci-lint run ./...` — zero issues.
- Configuration reference regenerated from the config schema.
