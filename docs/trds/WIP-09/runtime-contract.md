# Coordinator HA runtime contract

This contract describes the implemented Phase A/B/C path and supersedes the original proposal's pre-rewrite descriptions where they differ. The completion audit is in [acceptance.md](acceptance.md); Kubernetes configuration and storage requirements are in [kubernetes.md](kubernetes.md). Embedded consensus (Phase D), geo-replication and independent-disk metadata replication remain outside the original current-phase scope.

## Ownership and durable recovery

Single-node mode uses one Pebble metadata directory. Elected CLI modes use `HAService`, which starts its HTTP/RPC listeners before campaigning but opens the database only after election. Each grant gets a separate coordinator instance, caches, worker sessions and irrevocable storage handle. Losing authority fences new metadata operations immediately through that handle; admitted operations finish before it closes. Voluntary election release happens after metadata close. An old HTTP request or RPC session retains its old instance and cannot mutate a replacement term.

A standby never opens a second copy of the active metadata database. All candidates must use the same authoritative current directory with exclusive storage ownership. File-lock mode is for one host. Kubernetes mode can run candidates on different hosts only with the shared-storage and failed-client fencing guarantees in the deployment contract. A Lease is not a metadata replication protocol or a substitute for storage fencing. A periodic snapshot is not eligible for automatic takeover.

Recovery validates durable metadata and advances the epoch to at least the election grant and strictly beyond the stored token. It persists the effective value before publishing readiness, including when the election token is ahead of the database token. Corrupt or exhausted epoch records fail closed. File-lock companion epochs use atomic replacement, file fsync and directory fsync.

Recovery rebuilds job/worker indexes, preserves completed checkpoints, durably aborts unfinished checkpoints and fails unfinished savepoints. Worker heartbeat timestamps are deliberately ephemeral (WIP-08); recovered registrations are not proof of current authority. No production heartbeat-flush loop runs.

## Workers, fencing and recovery

`worker.coordinator_seeds` contains HTTP discovery addresses. A worker asks a seed for `/api/v1/cluster/leader`, follows at most one advertised leader hint, confirms that the named node is itself ready, and connects to `leader_rpc_addr`. Discovery does not extend the WIP-08 contact deadline and cannot grant execution authority; RPC registration and epoch validation do that.

HA workers require `worker.epoch_path`. The CLI defaults to `data/worker/epoch`; embedded callers must configure a durable path when using discovery. The file is exclusive to one worker process and must survive its restart. Each accepted registration epoch is atomically persisted and fsynced before the worker admits commands. A corrupt file or failed persistence stops startup/registration instead of resetting the fence. Persisting does not hold the worker mutex needed by the independent contact watchdog.

Workers cancel and join their prior execution attempt before registering a replacement session. For recovered assignments, the new coordinator waits either for that registration or until the old worker contact authority must have expired. Marking missing heartbeat history LOST/FAILED alone does not authorize overlapping execution. Existing checkpoint integrity, transactional fallback restrictions, restart limits/backoff and rescale rollback remain in force. A job without a usable recovery checkpoint can fail; HA does not manufacture missing state.

Epochs reject stale authority; they do not authenticate an arbitrary peer or validate fabricated higher tokens. Configure node TLS and access controls. Metadata persists the job configuration supplied by the client; `${ENV_VAR}` references are retained as supplied, and callers must not assume automatic redaction of literal credentials. Filesystem/volume encryption remains responsible for encryption at rest.

## Discovery and readiness

`http.adv_addr` and `node.rpc_advertise_addr` are distinct routable endpoints. Kubernetes mode requires both. Never advertise a wildcard listen address or a load-balanced address as an individual leader. Ephemeral ports in embedded tests are published after listeners bind.

- `/healthz`: process liveness, including standby.
- `/api/v1/cluster/leader`: leader identity, HTTP/RPC addresses, epoch, `is_self` and `ready`. A standby's record is only a hint; the worker confirms readiness at the advertised node.
- `/readyz`: 200 for the ready local leader, otherwise a redirect to a known other leader or 503. Kubernetes readiness must not mistake a followed redirect for local readiness.
- Standby application requests redirect to a known leader or return 503. Requests to a recovering local node return unavailable rather than redirecting to themselves.

On a Kubernetes API outage, renewal failure revokes authority before the lease takeover budget. The process closes its metadata handle and remains available to campaign; a failed API release leaves the remote lease to expire. Permanent authorization/configuration errors are surfaced. File-lock discovery ignores a leftover leader record when no process owns the lock.

## Same-host example

Start two coordinators with the same data directory and election lock, different node IDs and endpoints. Configure these fields in separate files:

```yaml
mode: coordinator
listen: 127.0.0.1:4102
node:
  id: coordinator-a
  data_dir: /absolute/shared/wire-metadata
  rpc_advertise_addr: 127.0.0.1:4102
http:
  addr: 127.0.0.1:4101
  adv_addr: 127.0.0.1:4101
election:
  backend: filelock
  lock_path: /absolute/shared/wire-election.lock
```

For coordinator B use `coordinator-b`, HTTP port 4201 and RPC port 4202; retain the data directory and lock path. Run each with `wire --config <its-config.yaml>`. Workers use `coordinator_seeds: ["127.0.0.1:4101", "127.0.0.1:4201"]` and a distinct durable `epoch_path` per worker. Keep heartbeat timeout settings consistent across all nodes. The worker supervisor must restart a worker that exceeds its contact deadline.

## Backup and disaster recovery

`MetadataStore.Snapshot(destination)` produces a consistent Pebble checkpoint; invoke it through the current owner's store handle rather than opening a competing live database. WIP-09 supplies the store primitive, not a new backup CLI or a configured periodic backup service (the proposal left snapshot frequency open).

For disaster recovery, fence/stop all old coordinators and workers before replacing the metadata directory with a verified snapshot. Retain epoch evidence; do not reset worker epoch files merely to force an older restored database to accept them. An old metadata snapshot can omit acknowledged jobs, newer checkpoints and committed external sink decisions. It therefore requires explicit operator reconciliation and cannot promise automatic exactly-once continuation. Normal HA takeover uses current authoritative storage and does not take this path.

## Compatibility audit still pending

The production CLI uses HAService. The preexisting embedded `Coordinator.Run` elected-store entry point remains under compatibility review because it accepts an already-open store and reuses coordinator state. Do not use that path to construct new HA deployments; the factory lifecycle is the tested implementation. The WIP remains under completion audit until this entry-point decision and final validation are resolved.
