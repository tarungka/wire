# WIP-09 implementation and acceptance plan

Work in progress, based on master `1ebb36e` (WIP-08). This is a requirements checklist, not a completion claim.

## Scope and implementation order

1. **Leadership-scoped storage:** open the authoritative Pebble directory only after election. A standby must start without taking its database lock. Fence reads/writes when the leadership context ends; drain in-flight access before close and release election only after releasing storage. Never recover automatically from a stale copied database. Preserve synchronous WAL/batch durability.
2. **Leadership lifecycle:** bind recovery, scheduling and mutations to the election context from the moment leadership is granted, including loss during recovery. Clear command streams and all term-local state on loss. Prevent operations from an old term from mutating a later term. Persist the effective fencing epoch before publishing readiness.
3. **Multi-host election:** implement the WIP's Kubernetes Lease backend with optimistic concurrency, bounded renewal, fail-closed expiry, leader discovery and RBAC/configuration/deployment documentation. Keep file-lock election for same-host deployments, harden its epoch persistence and standby discovery. Raft remains deferred. Multi-host storage requires a shared, strongly consistent, POSIX-lock-capable volume with storage fencing; a Lease alone cannot make independent local Pebble directories coherent.
4. **Discovery and fencing:** advertise separate HTTP and RPC addresses; discover the ready leader through configured coordinator seeds. Persist worker highest-seen epoch and reject stale leaders after restart. Retain WIP-08's contact deadline and fencing while reconnecting.
5. **Recovery:** reconcile all job states and restore running work from verified completed checkpoints; abort incomplete checkpoints/savepoints durably. Preserve transactional sink restrictions, rescale rollback and recovery budgets. Verify metadata identity and monotonically increasing epochs; fail closed on corruption/exhaustion.
6. **Operational contract:** working single-host and multi-host examples, storage/lease requirements, readiness/redirect behavior, restart/partition behavior, backup and restore procedure. Update the WIP's stale pre-rewrite descriptions (including heartbeat flush superseded by WIP-08), status/index only after completion.

## Required evidence

- Store interface coverage, durable reopen and snapshot validation, atomic batches and failure injection.
- Two independent coordinator instances sharing durable storage: concurrent startup, leader shutdown/crash, standby takeover, strictly increasing epoch, no simultaneous database owners.
- Loss during recovery and during mutation, storage failure, old-term delayed work, lease API conflicts/outage/expiry; stale command rejection including worker process restart.
- End-to-end worker discovery and checkpoint-restored job output after changing coordinator address; no manual resubmission.
- Recovery fixtures across CREATED/DEPLOYING/RUNNING/FAILING and completed/in-flight checkpoints; match/orphan/missing reconciliation.
- Benchmarks: 10,000-job recovery (<5 seconds target), durable writes and 1,000-job snapshots; measure takeover/restart against WIP targets.
- Full race suite, integration suite, build/vet/lint, configuration/reference consistency, and green follow-up PR CI.

The original claim that missing metadata in a stale snapshot can be repaired solely from worker reports is not a safe recovery guarantee. Workers do not reconstruct committed sink decisions. Only the authoritative current store is eligible for automatic takeover.

## Progress: storage ownership and lifecycle

- Added an irrevocable `LeadershipStore` handle per term; guarded operations stop on context revocation, and close joins admitted I/O.
- File-lock tokens now reject malformed/exhausted values and use fsynced atomic replacement. File-lock standbys can read the active leader's published endpoint record; unlocked stale records are ignored.
- CLI file-lock mode now uses `HAService`: election precedes opening Pebble, discovery remains available in standby, and each grant gets a new coordinator/cache/storage instance. RPC sessions bind once to that instance; delayed old requests cannot access a replacement term. The effective epoch is durably persisted, including when the election counter is ahead of stored metadata.
- Verified `TestHAServiceSharedMetadataTakeover`, `TestHALossDuringRecoveryNeverPublishesReady`, `TestFileLockStandbyDiscoversPublishedLeader`, leadership-store regressions and full coordinator/CLI race suites. This is same-host durable takeover evidence; it does not yet prove multi-host election or worker checkpoint restoration across addresses.
- Still pending: Kubernetes Lease implementation/configuration, routable advertise settings, worker multi-address discovery and durable epoch state, process/crash and full running-job takeover tests, benchmarks, final docs and CI.

## Progress: worker discovery and fencing

- Added `worker.coordinator_seeds`, `worker.epoch_path` (CLI default `data/worker/epoch`) and `node.rpc_advertise_addr`; HTTP advertisement uses the existing `http.adv_addr`. Each worker process must own a distinct epoch path. HA discovery requires durable fencing; direct embedded workers can retain ephemeral mode by omitting the path.
- Discovery queries seeds, treats standby records as hints, confirms the advertised node is ready, rejects older epochs and uses its RPC endpoint. Registration persists the accepted epoch before admitting commands; fsync does not hold the authority watchdog's mutex. Epoch files use exclusive ownership, atomic replacement and fsync; corrupt files fail startup without resetting the token.
- Recovery now waits for old workers to re-register (after their previous tasks join) or for their prior contact authority to expire. Stale/zeroed heartbeat metadata alone must not permit immediate overlapping redeployment.
- The four-package worker/coordinator/config/CLI race suite passed for discovery integration; focused recovery-fence and live failover verification follows. Generated configuration reference updated.

Kubernetes implementation reference: [Lease v1 API](https://kubernetes.io/docs/reference/kubernetes-api/coordination/lease-v1/) and [client-go leader election](https://github.com/kubernetes/client-go/blob/master/tools/leaderelection/leaderelection.go). Acquisition must use resource-version optimistic concurrency and locally observed elapsed time rather than trusting another host's wall-clock timestamp. Renewal must revoke local authority before another candidate may acquire the lease; storage ownership remains separately fenced.
