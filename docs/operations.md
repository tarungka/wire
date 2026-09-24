# Operations

**Status:** Canon
**Version:** 1.0.0
**Context:** Production & Runtime Management

---

## 1. Deployment Modes

Wire supports two primary deployment models.

### 1.1 Standalone Cluster
*   Manual start of `wire --mode coordinator` and `wire --mode worker`.
*   Best for bare-metal or VM-based deployments.
*   Configuration via `--config PATH` (YAML or JSON; default `.config/config.json`) and CLI overrides. See the [configuration reference](configuration-reference.md).

### 1.2 Kubernetes Native
*   **Wire Operator:** (Future) Manages the lifecycle.
*   Coordinator runs as a Deployment.
*   Workers run as a StatefulSet (providing stable network IDs and persistent volumes for Pebble local cache).

---

## 2. Scaling & Rescaling

Scaling in Wire implies changing the parallelism of the Job Graph.

### 2.1 The Rescaling Process

Rescaling is a stop-start operation through the existing job's REST endpoint:

1. Trigger a savepoint and poll until it is `COMPLETED`.
2. Submit `POST /api/v1/jobs/{job_id}/rescale` with a savepoint ID and either a
   global `parallelism` or an `operators` map.
3. The coordinator validates the topology, cancels the old attempt, redistributes
   supported keyed state, and deploys the new attempt. Observe job status and
   `rescale_failure`; acceptance is not completion.

```json
{"savepoint_id":"saved-id","operators":{"map-operator":8}}
```

Global rescale preserves source/sink counts and their Forward-connected groups.
Explicit changes must satisfy Forward-edge equality. Opaque state without a
redistribution contract and transactional state without a transaction-handle
mapping are not supported. Failed deployments can roll back subject to the
restart budget. See [rescale safety](rescale-safety.md) and the
[transaction contract](trds/WIP-10/runtime-contract.md).

Pause/resume endpoints are not a substitute: they do not yet implement completed
runtime suspension and redeployment from a savepoint.

## 3. Monitoring & Metrics

Managed HashMap state exposes `wire_state_backend_memory_bytes` per backend,
operator and task. It counts logical key/value payload, not RSS; see
[backend memory accounting](state-backend-selection.md#hashmap-memory-metric).

Wire exposes a Prometheus-compatible `/metrics` endpoint on a separate server
(default `:9090`, controlled by `--metrics-enabled` and `--metrics-addr`). Assign
unique metrics ports when running multiple nodes on one host.

### 3.1 Key Metrics

* **Backpressure:** `wire_task_backpressure_time_ms_total` records cumulative output wait.
* **Queue occupancy:** `wire_task_input_channel_usage` and `wire_task_output_channel_usage` report queued events, not utilization ratios.
* **Checkpoint replication:** `wire_task_checkpoint_upload_duration_ms` is a histogram of replication I/O duration.
* **Alignment:** `wire_checkpoint_alignment_time_ms` is a histogram; `wire_checkpoint_alignment_buffered_bytes` reports retained payload bytes.
* **Liveness:** `wire_workers_alive`, `wire_workers_lost_total`, and `wire_heartbeat_failures_total`.

Histograms expose `_bucket`, `_sum`, and `_count` series. See the
[observability guide](observability.md) for API/RPC/store instruments. Dedicated
end-to-end latency and records-processed metrics are not currently implemented.

### 3.2 Alerts
Critical alerts for production:
1.  **Checkpoint Failure:** If `last_completed_checkpoint` age > `2 * interval`.
2.  **Restart Loop:** Job restarts > 5 times in 1 hour.
3.  **Watermark Stall:** Watermark has not advanced for > 1 minute.

---

## 4. Configuration Tuning

### 4.1 Checkpoint Interval
*   **Low Interval (e.g., 1s):** faster recovery (replay less data), but higher I/O and network overhead.
*   **High Interval (e.g., 5m):** low overhead, but painful recovery (replaying 5m of source data).
*   *Recommendation:* Start with **10s to 30s**.

### 4.2 Pebble Tuning
*   **Block Cache:** Assign ~30-40% of Worker RAM to Pebble Block Cache.
*   **Compaction:** Monitoring write amplification. If high, increase L0 file size or thread count.

---

## 5. Common Failure Scenarios

| Scenario | System Behavior | Recovery Action |
| :--- | :--- | :--- |
| **Worker Crash** | Coordinator detects heartbeat loss. Marks Job `FAILING`. | Automatic Restart from last Checkpoint. |
| **Coordinator Crash** | Workers lose heartbeat. Workers self-terminate. | External Supervisor (K8s/Systemd) restarts Coordinator. Workers rejoin. |
| **Slow Sink** | Backpressure fills TCP buffers. Source slows down. | Scaling up Sink or increasing parallelism. |
| **Corrupt State** | Checksum fail on Pebble load. | Manual intervention: Restore from older Checkpoint. |

## Heartbeat health and worker loss

Configure `heartbeat.interval`, `heartbeat.timeout`, and `heartbeat.max_failures` on both node modes (defaults: `5s`, `30s`, `0`). Zero failures uses the elapsed timeout only; a positive count can stop a worker earlier after consecutive failed attempts. Keep coordinator and worker timeout settings consistent, including during rolling upgrades.

A closed coordinator session initiates prompt re-registration. If contact cannot be restored before the timeout, the worker stops admission and old tasks, closes transports and exits nonzero for its supervisor. Coordinator health checks run independently of job placement and restore affected jobs from their permitted checkpoints using the existing restart budget. Cluster status shows worker `ALIVE`/`LOST`; monitor `wire_workers_alive`, `wire_workers_lost_total`, `wire_heartbeat_latency_ms` and `wire_heartbeat_failures_total`.

Heartbeat resource samples are host measurements, collected asynchronously, and are not exposed on the public cluster API. Liveness and samples are not persisted. See the [complete heartbeat contract](trds/WIP-08/runtime-contract.md) for timing, payload measurement definitions, failover and compatibility.

## Coordinator high availability

Elected CLI modes open metadata only after election and isolate each leadership term. Same-host file-lock and Kubernetes Lease configurations, storage fencing requirements, advertised endpoints, worker discovery and backup limitations are specified in the [WIP-09 runtime contract](trds/WIP-09/runtime-contract.md). Kubernetes HA requires a shared authoritative metadata directory with exclusive, fenced storage access; see the [deployment and RBAC contract](trds/WIP-09/kubernetes.md). Do not use copied snapshots or independent local directories for automatic failover.

HA workers require a durable, per-worker `worker.epoch_path` and can discover coordinators through `worker.coordinator_seeds`. Preserve that file across process restarts. `/healthz` reports standby process health; a followed readiness redirect does not mean the local node is the leader.
