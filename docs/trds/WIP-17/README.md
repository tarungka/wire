# Technical Requirements Document (TRD)

> **Feature/Project:** `Heartbeat & Health Monitoring`
>
> **WIP ID:** `WIP-17`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-22`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Wire's architecture.md states "Workers send periodic heartbeats to the Coordinator" and "Timeout triggers a Job Failure event" but **specifies no interval, no timeout duration, no payload, and no configuration options**. Without this, developers cannot implement the heartbeat system, and operators cannot tune it for their network characteristics (e.g., high-latency cross-region deployments need longer timeouts).

### 1.2 Proposed Solution (Technical Summary)

Define the heartbeat protocol: workers send heartbeats every `heartbeat_interval` (default 5s) to the Coordinator over the RPC channel. Each heartbeat includes worker status, task statuses, resource utilization, and current load. The Coordinator marks a worker as lost after `heartbeat_timeout` (default 30s) of no heartbeats, which triggers job failure for all jobs with tasks on that worker.

---

## 2. Architecture & System Design

### 2.1 Heartbeat Flow

```
Worker                                  Coordinator
  │                                        │
  │  Heartbeat(WorkerID, tasks, load)      │
  ├───────────────────────────────────────▶│
  │                                        │ Reset timer for this worker
  │         HeartbeatResponse(commands)    │
  │◀───────────────────────────────────────┤
  │                                        │
  │  ... 5 seconds later ...               │
  │                                        │
  │  Heartbeat(WorkerID, tasks, load)      │
  ├───────────────────────────────────────▶│
  │                                        │
  │  ... worker crashes ...                │
  │                                        │
  │  [30s with no heartbeat]               │
  │                                        │ Timer expires
  │                                        │ Mark worker LOST
  │                                        │ Jobs on this worker → FAILING
```

### 2.2 Heartbeat Payload

```go
type Heartbeat struct {
    WorkerID        string
    Timestamp       int64                 // Worker's wall clock (Unix millis)
    TaskStatuses    []TaskStatus          // Status of each task running on this worker
    ResourceReport  ResourceReport        // CPU, memory, disk usage
    TaskSlotsTotal  int                   // Total task slots on this worker
    TaskSlotsInUse  int                   // Currently occupied task slots
}

type TaskStatus struct {
    TaskID          string
    JobID           string
    Status          string                // DEPLOYING | RUNNING | FINISHED | FAILED
    Metrics         TaskMetrics
}

type TaskMetrics struct {
    RecordsIn       int64
    RecordsOut      int64
    BytesIn         int64
    BytesOut        int64
    BackpressureMs  int64                 // Time spent blocked on output (last interval)
}

type ResourceReport struct {
    CPUUsagePercent    float64
    MemoryUsedBytes    int64
    MemoryTotalBytes   int64
    DiskUsedBytes      int64
    DiskTotalBytes     int64
    GoroutineCount     int
}
```

### 2.3 Heartbeat Response

The Coordinator can piggyback commands on the heartbeat response:

```go
type HeartbeatResponse struct {
    Commands []WorkerCommand
}

type WorkerCommand struct {
    Type    string   // "CANCEL_TASK" | "DEPLOY_TASK" | "TRIGGER_CHECKPOINT"
    Payload []byte   // Command-specific payload
}
```

---

## 3. API Design

### 3.1 Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `heartbeat.interval` | `5s` | How often workers send heartbeats |
| `heartbeat.timeout` | `30s` | Time without heartbeat before worker declared lost |
| `heartbeat.max_failures` | `0` | Consecutive failures before declaring loss (0 = use timeout only) |

```yaml
# wire.yaml
heartbeat:
  interval: "5s"
  timeout: "30s"
```

### 3.2 Failure Detection Cascade

1. Worker misses heartbeat → Coordinator starts countdown.
2. After `heartbeat_timeout` with no heartbeat → Worker marked **LOST**.
3. All tasks on lost worker → status **FAILED**.
4. All jobs with failed tasks → status **FAILING**.
5. Coordinator initiates recovery per job's restart strategy (see WIP-04).

### 3.3 Worker Self-Termination

If a worker loses contact with the Coordinator (no heartbeat response for `heartbeat_timeout`), it self-terminates:
1. Stops processing all tasks.
2. Closes all Yamux connections.
3. Exits with non-zero status (for supervisor restart).

This prevents split-brain scenarios where a worker continues processing while the Coordinator has already rescheduled its tasks.

### 3.4 Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `wire_heartbeat_latency_ms` | Histogram | Round-trip time for heartbeat RPC |
| `wire_heartbeat_failures_total` | Counter | Failed heartbeat attempts (network errors) |
| `wire_workers_alive` | Gauge | Number of workers with active heartbeat |
| `wire_workers_lost_total` | Counter | Workers declared lost |

---

## 4. Data Model & Storage

Heartbeat state is ephemeral — maintained in Coordinator memory:

| Field | Type | Description |
|-------|------|-------------|
| worker_id | string | Worker identifier |
| last_heartbeat | timestamp | Time of last successful heartbeat |
| status | enum | ALIVE / LOST |
| task_slots | int | Available task slots |
| resource_report | ResourceReport | Latest resource usage |

Heartbeat state is **not** persisted to the Raft log (too frequent, too ephemeral). On Coordinator failover, workers must re-register via the next heartbeat cycle.

---

## 5. Design Decisions & Trade-offs

### Decision 1: 5s interval / 30s timeout (6x ratio)

|  |  |
| -- | -- |
| **Context** | Interval and timeout determine detection speed vs false positive rate. |
| **Options Considered** | (A) 1s / 5s (fast detection, more network traffic), (B) 5s / 30s (balanced), (C) 10s / 60s (conservative) |
| **Decision** | Option B: 5s / 30s |
| **Rationale** | 6 missed heartbeats before declaration gives resilience against network blips and GC pauses. 30s detection is fast enough for most workloads. Matches Kafka consumer group defaults. |
| **Trade-offs Accepted** | 30 seconds of stalled processing before recovery starts. |
| **Revisit Trigger** | If users need sub-10s failure detection. Allow per-job configuration. |

### Decision 2: Worker self-termination on coordinator loss

|  |  |
| -- | -- |
| **Context** | Workers that can't reach the Coordinator are in an ambiguous state. |
| **Options Considered** | (A) Worker self-terminates, (B) Worker continues processing optimistically, (C) Worker pauses and waits |
| **Decision** | Option A: Self-terminate |
| **Rationale** | Prevents split-brain. If the Coordinator has rescheduled the worker's tasks to another node, both nodes processing the same data violates exactly-once. Self-termination + external supervisor (systemd, K8s) provides clean restart. |
| **Trade-offs Accepted** | Transient network partition kills the worker (even if it could have reconnected). |
| **Revisit Trigger** | If network partitions are frequent. Consider a "grace period" before self-termination. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Network partition between worker and coordinator | Worker self-terminates after timeout. Coordinator marks worker lost. Tasks rescheduled. | Brief downtime for affected jobs | High |
| 2 | Coordinator failover during heartbeat | Worker's next heartbeat goes to new leader. Worker re-registers automatically. | One missed heartbeat cycle | Low |
| 3 | Worker GC pause > heartbeat timeout | Worker declared lost. After GC, worker self-terminates (no coordinator response). | False positive | Medium |
| 4 | Clock skew between worker and coordinator | Heartbeat uses wall-clock round-trip, not timestamp comparison. Clock skew doesn't affect detection. | No impact | Low |
| 5 | All workers lost simultaneously | All jobs enter FAILING. Coordinator waits for workers to rejoin (new or restarted). | Full cluster outage | Critical |

---

## 7. Security & Compliance

### 7.1 Heartbeat Authentication

Heartbeat RPC uses the same authentication as other Coordinator-Worker RPCs (mTLS on port 4002). See WIP-09.

### 7.2 Resource Report Privacy

ResourceReport includes system-level metrics (CPU, memory, disk). These are not externally exposed — only accessible to the Coordinator for scheduling decisions.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Timer logic, timeout detection, status transitions | Go `testing` | 100% |
| Integration Tests | Worker → heartbeat → Coordinator | MiniCluster | Normal + timeout + recovery |
| Chaos Tests | Kill worker, network partition | toxiproxy | Detection latency < timeout + 1s |

### 8.1 Key Test Scenarios

1. Normal: Worker heartbeats → Coordinator tracks status → worker marked ALIVE
2. Worker crash: Stop heartbeats → wait timeout → verify worker marked LOST, jobs FAILING
3. Worker self-termination: Block coordinator response → verify worker exits after timeout
4. Recovery: Worker lost → restart → new heartbeat → worker marked ALIVE, tasks rescheduled

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should heartbeat interval be configurable per-worker (not just globally)? | Tarun | Open |
| 2 | Should we use Raft's built-in heartbeat instead of a separate mechanism? | Tarun | Open |
| 3 | Risk: GC pauses in Go can exceed 5s on large heaps. Default timeout may need tuning for large workers. | — | Acknowledged |
