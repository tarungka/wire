# Operations

**Status:** Canon
**Version:** 1.0.0
**Context:** Production & Runtime Management

---

## 1. Deployment Modes

Wire supports two primary deployment models.

### 1.1 Standalone Cluster
*   Manual start of `wire coordinator` and `wire worker`.
*   Best for bare-metal or VM-based deployments.
*   Configuration via `wire.yaml`.

### 1.2 Kubernetes Native
*   **Wire Operator:** (Future) Manages the lifecycle.
*   Coordinator runs as a Deployment.
*   Workers run as a StatefulSet (providing stable network IDs and persistent volumes for Pebble local cache).

---

## 2. Scaling & Rescaling

Scaling in Wire implies changing the parallelism of the Job Graph.

### 2.1 The Rescaling Process
Wire does **not** support "hot" dynamic scaling. Rescaling is a stop-start action:

1.  **Trigger Savepoint:** Operator triggers a manual global checkpoint (Savepoint).
2.  **Stop Job:** The job is cancelled.
3.  **Update Config:** Change parallelism (e.g., 4 -> 8).
4.  **Restart:** Submit the job pointing to the Savepoint path.
5.  **State Rebalancing:**
    *   The Coordinator recalculates Key Group assignments.
    *   New workers download the specific Key Groups they now own from the Savepoint.

---

## 3. Monitoring & Metrics

Wire exposes a Prometheus-compatible `/metrics` endpoint on all nodes.

### 3.1 Key Metrics
*   **Throughput:** `wire_records_processed_total` (per operator).
*   **Latency:** `wire_end_to_end_latency_ms` (time from Source timestamp to Sink).
*   **Backpressure:** `wire_buffer_usage_ratio` (if > 0.9, downstream is slow).
*   **Checkpointing:**
    *   `wire_checkpoint_duration_ms` (Async upload time).
    *   `wire_checkpoint_alignment_time_ms` (Time spent waiting for barriers).
    *   `wire_last_completed_checkpoint_id`.

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
