# WIP-08 implementation and acceptance audit

Linked follow-up to merged [#201](https://github.com/tarungka/wire/pull/201). Base: `d74f4d9`, including WIP-07 (#220). See [runtime-contract.md](runtime-contract.md) for semantics and compatibility.

| Requirement | Implementation | Evidence |
| --- | --- | --- |
| Configurable interval, timeout, failure limit; 5s/30s/0 defaults | Config schema, defaults, validation, both CLI node modes, RPC sender and coordinator timing | TestHeartbeatConfiguration; generated configuration reference |
| Periodic heartbeat, reset on success, bounded no-response detection | Elapsed contact deadline plus bounded method call and optional consecutive-failure limit | TestHeartbeatContactDeadlineBoundsHungRPC; TestHeartbeatZeroFailureLimitUsesElapsedTime; existing sender interval, reset and callback tests |
| ALIVE → LOST, all unfinished task failures, jobs FAILING, no stale revival | Independent coordinator timer, atomic loss marking, session/epoch fencing and recovery scheduling | TestLostWorkerCannotReviveWithoutRegistration; TestRestartDoesNotWaitForExpiredWorker; TestHeartbeatRefreshesOnlyCurrentEpoch; TestRegistrationBindsLegacyAndCurrentSessionsAtomically; TestWorkerLossRetriesDurableTransitionAfterStorageFailure |
| Actual worker loss and checkpoint recovery on a survivor | Worker termination, survivor restore through real replica RPCs | TestClusterCheckpointRestartsAfterWorkerLoss (three race runs, detection bounded by timeout + 1s); existing restart-budget and checkpoint integrity/transaction tests |
| Coordinator failover and re-registration | Prompt closed-session reconnect; process-wide watchdog retained across failed reconnects | TestClusterCheckpointCoordinatorFailover; TestWorkerReregistersAfterEpochChange |
| Worker stops processing, closes sessions, returns fatal error after loss | Admission fence, task cancellation, transport closure and bounded join | TestHeartbeatPartitionStopsActiveWorker (three race runs); TestWorkerContactDeadlineIncludesFailedDials |
| Task identity/status, slots, throughput and backpressure | Attempt-local atomic counters; live handle and reservation snapshot | TestHeartbeatCarriesAttemptStatusAndTaskMetrics; TestTaskStatisticsCountSuccessfulOutputsAndBlockedTime; existing reservation accounting tests |
| CPU, memory, disk and goroutine reports | Background cross-platform gopsutil sampler, sample timestamps and explicit unavailable fields | TestResourceReportSamplesHostAndMarksUnavailable; CPU delta assertion |
| Four production heartbeat metrics | OTel sender instrumentation and coordinator loss counter/alive callback | TestHeartbeatInstruments verifies names, values, units and zero alive count |
| Ephemeral heartbeat state and stale recovery | No periodic heartbeat storage writes; registration excludes receipt timestamp | TestCoordinator_HeartbeatStateIsEphemeral; existing metadata recovery/legacy advisory-key tests |
| Clock skew, missing responses, repeated loss, all-worker loss | Local monotonic timestamps; rejected stale heartbeats; placement consumes budget only when published | Future sender timestamp in deadline/fencing tests; TestHeartbeatAuthorityUsesRequestSendTime; TestTrackerRejectsLateHeartbeatBeforeDetectionTick; TestAllWorkersLostWaitsWithoutSpendingRecoveryBudget |
| Authentication and private resource reports | WIP-07 mTLS identity guard retained; resource reports not exposed by cluster endpoint | Existing mutual TLS tests; explicit nodeResponse schema |

## Validation

- Full `go test -race ./...` passed. Subsequent conservative request-send-time renewal and atomic registration/session-binding changes passed the affected heartbeat, mTLS and live failover tests under `-race`.
- Heartbeat timer/state regressions passed five race-detector repetitions; intermediate-state assertions use explicit clock checks instead of sleep timing.
- Linux amd64 with CGO disabled cross-compilation passed.
- Worker-loss recovery and active-worker partition regressions passed three race-detector repetitions each.
- A built CLI process using 20ms/200ms heartbeat timing against an unreachable coordinator exited with code 1 and the contact-deadline error in 0.648s (including process startup).
- `go build ./...`, `go vet ./...`, lint (zero issues) and whitespace checks passed.
- Tests use real TCP/Yamux/TLS and controlled handler failures. Docker/toxiproxy was not run; no container-specific result or 100% line-coverage claim is made.

The runtime contract explicitly documents consistent node configuration, host rather than container-quota resource accounting, asynchronous sample age, and cooperative user-code cleanup. These do not suppress loss detection or permit reuse of expired execution authority.

Worker-loss failure publication shares the ownership lock with detection, so a concurrent re-registration cannot turn an old loss into a failure of the replacement attempt. A metadata outage leaves the failure pending for retry rather than publishing an undurable job state. The storage-failure regression fails against the previous implementation and passes five runs under `-race`; worker-loss recovery and coordinator failover tests also pass with this guard.

Review follow-up: the 250ms health timer and expired-heartbeat rejection path now only fence workers in memory and wake the scheduler. Per-job assignment reads and durable recovery remain on the scheduler path under the ownership lock. `TestWorkerExpiryDefersAssignmentIOToScheduler` rejects any metadata I/O on the fast path and verifies deferred task/job recovery; the durable-transition retry regression remains in place.

Scope notes from review: `rpc.HeartbeatTracker` is not used by the production coordinator; its unit tests are supplementary, not evidence for the production loss path. Production evidence is the coordinator liveness and worker-loss recovery tests above. Removing that duplicate helper and defining cancellation/fencing semantics for cluster-API node removal remain follow-ups. `runWorker` joins both errgroup goroutines before returning; worker shutdown itself retains a five-second bound. Host resource diagnostics remain intentional, with the gopsutil dependency and host-versus-container limitation described in the runtime contract.
