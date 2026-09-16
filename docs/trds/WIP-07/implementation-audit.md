# WIP-07 implementation and acceptance record

Implemented on master baseline `0d5cf90` (2026-09-15), following merged #202. This record covers the code in this change; publication as a PR does not itself mean the change is merged.

| Requirement | Implementation | Acceptance evidence |
| -- | -- | -- |
| All six RPC signatures, direction, errors, deadlines and retry contracts | `runtime-contract.md`; concrete codec-tagged types in `internal/rpc/messages.go`; endpoint registrations in coordinator transport and worker session | `TestServerAllSixRPCs`, real cluster lifecycle below, reviewed contract against registered methods and callers |
| Resource negotiation and expiry | Worker `reservations.go`; scheduler `reserveDeployment`; stable job/epoch/attempt identity, query/release, 30s default lease, no overbooking | `TestWorkerReservationsBoundExpireAndFence`, `TestReservedCapacitySurvivesHeartbeatAccounting`, `TestReservationFailureDoesNotPublishDeployment` |
| Durable acknowledged deployment and idempotency | Worker validates full task list and atomically consumes lease; scheduler writes assignment/grants before RPC; completed-attempt receipts prevent reexecution | `TestReservedDeploymentRetryDoesNotReexecuteCompletedTask`, `TestScheduleJob_AtomicDeployment`, `TestReservedJobRunsOverMutualTLS` |
| Uncertain reply/cancellation safety | Absent-task cancellation acknowledgements and session-local attempt tombstones; existing attempt fencing and task teardown | `TestWorkerCancelledReservationCannotDeployLate`, existing cancellation fencing and restart tests |
| Checkpoint RPC workflow | Direct acknowledged trigger on capable workers; legacy push fallback; existing durable ACK publication and abort/commit commands | `TestTriggerCheckpointRPCIsIdempotentAndFenced`, `TestClusterCheckpointReplicatesAndCompletes`, `TestClusterCheckpointTransactionalCommit`, `TestClusterCheckpointTransactionalAbort`, checkpoint failure/timeout tests |
| Restore/failover safety | Existing WIP-05/06 grants and manifests unchanged; all cluster workers now advertise/use reservations | `TestClusterCheckpointRestartsFromReplica`, `TestClusterCheckpointCoordinatorFailover`, missing/corrupt/four-missing-archive fallback and transactional-fallback refusal tests |
| Framing, limits and correlation | Existing bounded frame reader plus unary/streaming method/ID validation; panic replies retain ID | Codec roundtrip/boundary/truncation/oversize tests, `TestRPCRejectsMismatchedResponse`, `TestServerHandlerPanic` |
| Cancellation, concurrency and shutdown | Server setup/method deadlines; peer-close cancellation; bounded client pending opens; independent session tracking | `TestUnaryHandlerCancelledWhenCallerLeaves`, `TestUnaryServerEnforcesMethodDeadline`, `TestServerConcurrentRPCs`, `TestCancelledCallPreservesSharedSession`, `TestCancelledOpenBoundsPendingWork`, `TestServerStopsAllSessions` |
| Safe bounded retry and monotonic status | Retryable envelopes and safe identified transport retries; bounded hints/backoff; no terminal-status regression | `TestIdempotentRPCRetriesLostReply`, `TestTerminalTaskRetryCannotResurrectAttempt`, existing retry/cancellation tests |
| Heartbeat protocol and recovery interaction | Five-second sender, configured contact-loss threshold, coordinator live-worker checks, WIP-21 push plus heartbeat fallback | Heartbeat sender interval/dispatch/contact-loss tests; tracker ALIVE/SUSPECT/DEAD tests; `TestRestartDoesNotWaitForExpiredWorker`; existing worker reconnect/failover cluster tests |
| RPC transport security and method roles | Node TLS flags wired to both RPC endpoints; TLS 1.3; verified worker certificate CN checked for each identity-bearing method; separate endpoint method tables | `TestCoordinatorRPCMutualTLSAndWorkerIdentity`, `TestRPCIdentityMatchesVerifiedCertificate`, `TestReservedJobRunsOverMutualTLS`, `TestRPCDoesNotSilentlyDisableRequestedTLS`, `TestServerUnknownMethod` |
| Mixed-version and specification reconciliation | Explicit capability gate and legacy command path; current runtime contract overrides obsolete protobuf fields, heartbeat-only dispatch and ID high-water replay assumptions | Legacy scheduler tests still use workers without capability; full cluster tests use capability-bearing workers; runtime contract and WIP status index updated |

## Validation

- Full `go test -race ./...` passed after fixing server deadline/cancellation ordering. Deadline and peer-cancellation regressions also passed ten repetitions under the race detector.
- Subsequent checkpoint trigger admission changes passed the affected worker checkpoint tests and explicit duplicate/stale/absent-task trigger regression under `-race`.
- `go build ./...`, `go vet ./...`, `golangci-lint run ./...` (zero issues), and `git diff --check` passed.
- Tests use real TCP/TLS/Yamux cluster connections and in-memory pipes for deterministic lost replies/cancellation. A Docker/toxiproxy deployment was not run; no container-specific result is claimed.

## Scope boundaries and decisions

The six-method workflow is implemented, not just defined. Reservation IDs deliberately make retries idempotent rather than reserving again as the original non-idempotent sketch proposed. Allocation does not promise memory quotas: positive memory requests explicitly return INSUFFICIENT_RESOURCES and slot resource counts are reported. Deployment admission is acknowledged before potentially slow restore; RUNNING is emitted only after initialization, preserving WIP-02/03's asynchronous lifecycle.

WIP-21 remains the command push mechanism for cancellation and checkpoint decisions. WIP-08 owns broader health policy/configuration and presentation; this change documents current runtime timing rather than claiming the proposal's obsolete 15-second timeout. WIP-17 retains HTTP/RBAC, data-plane and checkpoint-replica security: this change secures the coordinator-worker RPC surface when configured, not the entire cluster. Development without TLS remains explicitly unauthenticated. Optional compression, batching, generated protobuf structs and alternative leader discovery remain the original open design questions, not implemented promises.

## PR #220 review fixes

- Reserved redeployments wait for the previous handle's teardown and revalidate admission without consuming the lease early. `TestReservedDeploymentWaitsForPreviousTeardown` covers successful handoff, context cancellation, lease expiry, cancellation fencing and completed-attempt replay.
- Scheduling runs bounded independent per-job operations. `TestSchedulerHungReservationDoesNotBlockOtherJobs` covers a newly submitted job progressing while another worker's reservation handler hangs, duplicate scheduling suppression and shutdown cancellation/join.
- Absent-task cancellation acknowledgements run asynchronously with bounded concurrency and duplicate coalescing. `TestAbsentCancellationDoesNotBlockCommands` holds the status handler open and verifies later cancellation still executes.
- Successful submissions no longer make redundant release calls. Receipt/tombstone retention remains session-long to preserve replay safety; the runtime contract records the memory-growth limitation and prerequisite for safe compaction.

Review validation: all three new regressions fail when their respective old behaviours are restored, and pass five repetitions with the fixes under `-race`. The full `go test -race ./...`, build, vet and lint pass.
