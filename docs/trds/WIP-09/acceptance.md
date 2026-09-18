# WIP-09 acceptance audit

Audit in progress. This records evidence by requirement; it is not yet a completion declaration. Base: master `1ebb36e`. Current implementation history and outstanding items are recorded in [implementation-plan.md](implementation-plan.md).

| Requirement | Implementation and direct evidence | Assessment |
|---|---|---|
| Atomic batch interrupted by process death | `TestPebbleBatchCrashIsAtomic` persists a partial WAL write inside production WriteBatch, kills the child before Commit returns, and verifies all three previous values on reopen | Verified three race repetitions |
| Durable metadata store; Get/Set/Delete/WriteBatch/PrefixScan/Snapshot/Close | Existing Pebble interface tests; `TestLeadershipStoreRevocationAndDurableTakeover`; `TestLeadershipStoreCloseJoinsAdmittedWrite`; 1,000-job snapshot reopen and byte comparison benchmark | Verified API operations; no claim of 100% source-line coverage |
| Election before storage open; standby remains available | `HAService`, `TestHAServiceSharedMetadataTakeover`, `TestHACrashReopensDurableMetadataInStandbyProcess` (standby health is checked before killing leader) | Verified |
| Epoch monotonicity, corruption/exhaustion, effective election token persisted | `TestFileLockElectionRejectsInvalidEpochWithoutOverwriting`, `TestRecoveryRejectsInvalidEpochWithoutOverwrite`, `TestRecoveryPersistsEffectiveElectionEpoch` | Verified |
| No old-term storage reuse; loss during recovery | `TestLeadershipStoreRevocationAndDurableTakeover`, `TestLeadershipStoreRevokedDuringOpen`, `TestHALossDuringRecoveryNeverPublishesReady` | Verified on HAService path; `TestLifecycle_ElectedRunRequiresHAService` checks the old entry point fails before campaigning or changing metadata |
| Kubernetes Lease ownership and renewal | `TestKubernetesLeaseCompetingCandidatesAndDiscovery`, `TestKubernetesLeaseAPIFailureRevokesAndExpires`, `TestKubernetesLeaseHungRenewalRevokesAuthority`, `TestKubernetesLeaseDeniedCredentialsFailCampaign` | Verified through HTTPS Lease API fixture |
| Standby HTTP/RPC discovery and ready leader confirmation | `TestFileLockStandbyDiscoversPublishedLeader`, `TestDiscoveryConfirmsLeaderAndSkipsStaleSeeds`, `TestDiscoveryRejectsUnreadyLeader`, `TestDiscoveryCancellationStopsBlockedSeed` | Verified |
| Worker fencing survives restart, stale leader cannot admit commands | `TestWorkerEpochSurvivesRestartAndRejectsRegression`, `TestWorkerEpochCorruptionFailsClosed`, `TestRestartedWorkerAdvertisesPersistedEpochAndRejectsOlderLeader` plus existing worker epoch/reservation tests | Verified |
| Recover job states and reconcile match/orphan/missing tasks | `TestRecovery_AllJobStates` and existing reconcile tests; `TestRecoveryWaitsForPreviousWorkerAuthority` | Verified; old authority must expire or be replaced after teardown |
| Running jobs restore from completed checkpoints across leader/address changes | `TestHAJobRestoresThroughDiscoveryAtDifferentAddress`, `TestHAJobRestoresThroughKubernetesLeaseDiscovery` | Verified with two coordinators, two workers/replica servers, durable Pebble, restored source/sink and resumed sink writes |
| Process crash, 100 jobs/50 RUNNING/10 completed checkpoints and abort incomplete checkpoint | `TestHACrashReopensDurableMetadataInStandbyProcess` uses an OS-killed child, live standby and durable reopen | Verified three race repetitions |
| Restart <10s; failover <15s | Process takeover 184–194ms; checkpoint-restored running job 2.13–2.32s, Kubernetes API-backed version 2.25–2.28s | Verified locally; not a network-storage SLA |
| 10,000-job recovery <5s | `BenchmarkHARecover10000Jobs`, 10,000 jobs with 1KiB config each and one completed checkpoint each: 61–77ms | Verified on Apple M4/Darwin arm64, three one-iteration measurements |
| Snapshot/write benchmarks | `BenchmarkHASnapshot1000Jobs`: 31–36ms; verified 2,000 keys after reopen. `BenchmarkHADurableMetadataWrite`: 3.84–3.88ms per synced 1KiB write | Measured locally, not sustained-throughput claims |
| Storage/election operational requirements, RBAC, discovery config, backup limitations | [Runtime contract](runtime-contract.md), [Kubernetes deployment](kubernetes.md), generated configuration reference | Written; final doc reconciliation pending |
| Full validation and linked follow-up PR | Full race suite, integration-tagged suite, build, vet and lint passed after redirect epoch-header and election-grant fixes | Exact PR-head CI pending |

## Scope and limits carried from the design

Phase D/Raft, multi-region HA, independent-disk replication, data-plane replication and Kubernetes operator/Helm packaging are not introduced by this WIP. Multi-host candidates require authoritative storage with failed-client I/O fencing; neither an RWX label nor successful Lease tests certifies a storage driver. API tests use a real HTTPS fixture, not a real Kubernetes cluster/storage partition. The process tests use independent local processes; job tests use independent coordinator endpoints and production TCP/RPC/worker paths.

Historical heartbeat persistence is superseded by WIP-08: timestamps are ephemeral, and recovery waits for registration or authority expiry. Tests of the retained legacy heartbeat-flush helper do not prove a production flush service. The historical suggestion that worker reports repair a stale metadata snapshot is rejected: reports cannot reconstruct missing committed sink decisions. Snapshots remain an operator disaster-recovery primitive, not automatic standby replication.
