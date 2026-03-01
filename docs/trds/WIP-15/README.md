# Job Lifecycle & REST API

> **Feature/Project:** `Job Lifecycle & REST API`
>
> **WIP ID:** `WIP-15`
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

Wire's architecture docs describe a `CREATED → DEPLOYING → RUNNING → FINISHED` task lifecycle state machine, but there is **no documentation on how jobs are submitted, started, paused, canceled, upgraded, or queried**. There is no REST API spec, no CLI command reference for job management, and no description of the savepoint mechanism for planned rescaling or upgrades.

### 1.2 Proposed Solution (Technical Summary)

Define the complete job lifecycle state machine with all transitions, a REST API on the Coordinator's HTTP port (4001) for job CRUD, savepoint management, and cluster inspection, and CLI commands that wrap the REST API. Jobs transition through CREATED → DEPLOYING → RUNNING → (PAUSED/FAILING) → CANCELED/FINISHED with well-defined triggers and recovery semantics.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define complete job state machine with all transitions | Job scheduling/cron triggers |
| Specify REST API for job management (CRUD, savepoints) | GraphQL API |
| Define CLI commands for job operations | Web UI for job management |
| Document savepoint/upgrade workflow | Hot code upgrade without restart |
| Specify cluster status and node management endpoints | Multi-cluster federation |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Job lifecycle states documented | Partial (task states only) | Complete (job + task) | Doc review |
| REST API endpoints documented | 0 | All CRUD + savepoint + cluster | API review |
| User can manage jobs from docs | Impossible | Possible | Manual walkthrough |

---

## 2. Architecture & System Design

### 2.1 Job State Machine

```
                    +-----------+
                    |  CREATED  |
                    +-----+-----+
                          |
                    SubmitJob()
                          |
                    +-----v-----+
              +---->| DEPLOYING |
              |     +-----+-----+
              |           |
              |     All tasks RUNNING
              |           |
              |     +-----v-----+
              |     |  RUNNING  |<------+
              |     +-----+-----+       |
              |       |   |   |         |
              |  Pause|   |   |Failure  |
              |       |   |   |detected |
         Restart +----v+  | +-v-------+ |
         from    |PAUSED| | | FAILING |-+
         savepoint+-----+ | +-+-------+
              |            |   |
              |      Cancel|   |Recovery
              |            |   |exhausted
              |      +-----v---v--+
              |      |  CANCELED  |
              |      +------------+
              |
              |     +------------+
              +-----|  FINISHED  |
                    +------------+
```

### 2.2 State Definitions

| State | Description | Automatic Transition |
|-------|-------------|---------------------|
| **CREATED** | Job graph validated, registered. No resources allocated. | None (awaits submit) |
| **DEPLOYING** | Tasks scheduled to workers. State being restored from checkpoint/savepoint. | → RUNNING when all tasks report running |
| **RUNNING** | All tasks actively processing events. Checkpoints occurring. | → FINISHED when bounded sources exhaust |
| **PAUSED** | Processing suspended. State preserved in savepoint. Resumes on user command. | None (awaits resume) |
| **FAILING** | Failure detected. Automatic recovery in progress per restart strategy. | → DEPLOYING (if restart budget remains) or → CANCELED |
| **CANCELED** | Stopped by user or exhausted restart attempts. Terminal state. | None |
| **FINISHED** | All sources exhausted, all data flushed to sinks. Terminal state. | None |

### 2.3 Data Flow

**Job Submission:**
1. User sends pipeline config (YAML) or compiled binary to Coordinator REST API.
2. Coordinator validates the config and builds the StreamGraph.
3. Coordinator optimizes StreamGraph → JobGraph.
4. Job enters CREATED state. Response includes `job_id`.
5. Coordinator calculates ExecutionGraph (parallel task instances).
6. Job enters DEPLOYING. Tasks assigned to workers.
7. Workers restore state (if savepoint provided), start processing.
8. All tasks report RUNNING → Job enters RUNNING.

**Savepoint → Upgrade:**
1. User triggers savepoint via `POST /api/v1/jobs/{id}/savepoints`.
2. Coordinator injects barriers (same as checkpoint but user-triggered).
3. All tasks snapshot, upload to durable storage.
4. Savepoint marked COMPLETED with a path.
5. User cancels old job.
6. User submits new job with `--savepoint` pointing to the savepoint path.
7. New job restores from savepoint (same semantics as checkpoint recovery).

---

## 3. API Design

### 3.1 Endpoint: `POST /api/v1/jobs`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs` |
| **Auth** | Bearer JWT (see WIP-17) |
| **Description** | Submit a new job from a YAML pipeline configuration |

**Request Body** (Content-Type: application/yaml)

```yaml
name: "user-event-counter"
parallelism: 4
sources: [...]
transforms: [...]
sinks: [...]
```

**Response (201 Created)**

```json
{
  "job_id": "job-a1b2c3d4",
  "name": "user-event-counter",
  "status": "CREATED",
  "created_at": "2024-01-15T10:30:00Z"
}
```

**Error Responses**

| Code | Error | Description & When |
| -- | -- | -- |
| 400 | INVALID_CONFIG | Pipeline YAML is malformed or fails validation |
| 401 | UNAUTHORIZED | Missing or invalid auth token |
| 409 | JOB_EXISTS | A job with the same name already exists and is not in a terminal state |

---

### 3.2 Endpoint: `POST /api/v1/jobs/submit`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs/submit` |
| **Auth** | Bearer JWT |
| **Description** | Submit a compiled Wire job binary |

**Request Body** (Content-Type: multipart/form-data)

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `jar` | file | Yes | Compiled Wire job binary |
| `parallelism` | int | No | Override default parallelism |
| `savepoint` | string | No | Path to savepoint to restore from |
| `args` | string | No | Additional job arguments |

**Response (201 Created)**

```json
{
  "job_id": "job-e5f6g7h8",
  "status": "DEPLOYING",
  "created_at": "2024-01-15T10:30:00Z"
}
```

---

### 3.3 Endpoint: `GET /api/v1/jobs`

| Field | Value |
| -- | -- |
| **Method** | GET |
| **Path** | `/api/v1/jobs` |
| **Auth** | Bearer JWT |
| **Description** | List all jobs, optionally filtered by status |

**Query Parameters**

| Param | Type | Description |
|-------|------|-------------|
| `status` | string | Filter by status (e.g., `RUNNING`) |

**Response (200 OK)**

```json
{
  "jobs": [
    {
      "job_id": "job-a1b2c3d4",
      "name": "user-event-counter",
      "status": "RUNNING",
      "parallelism": 4,
      "created_at": "2024-01-15T10:30:00Z",
      "started_at": "2024-01-15T10:30:02Z"
    }
  ]
}
```

---

### 3.4 Endpoint: `GET /api/v1/jobs/{job_id}`

| Field | Value |
| -- | -- |
| **Method** | GET |
| **Path** | `/api/v1/jobs/{job_id}` |
| **Auth** | Bearer JWT |
| **Description** | Get detailed job status including per-task metrics |

**Response (200 OK)**

```json
{
  "job_id": "job-a1b2c3d4",
  "name": "user-event-counter",
  "status": "RUNNING",
  "parallelism": 4,
  "created_at": "2024-01-15T10:30:00Z",
  "started_at": "2024-01-15T10:30:02Z",
  "config": { "..." : "..." },
  "tasks": [
    {
      "task_id": "task-0",
      "operator": "api-source",
      "subtask_index": 0,
      "status": "RUNNING",
      "worker_id": "worker-1",
      "metrics": {
        "records_in": 1523400,
        "records_out": 1523400,
        "bytes_in": 304680000
      }
    }
  ],
  "checkpoints": {
    "latest_completed": 42,
    "latest_duration_ms": 1250,
    "total_completed": 42,
    "total_failed": 0
  }
}
```

**Error Responses**

| Code | Error | Description & When |
| -- | -- | -- |
| 404 | NOT_FOUND | No job with this ID exists |

---

### 3.5 Endpoint: `POST /api/v1/jobs/{job_id}/cancel`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs/{job_id}/cancel` |
| **Auth** | Bearer JWT |
| **Description** | Cancel a running or paused job. Optionally trigger a savepoint first. |

**Query Parameters**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `savepoint` | bool | false | Trigger savepoint before canceling |

**Response (200 OK)**

```json
{
  "job_id": "job-a1b2c3d4",
  "status": "CANCELED",
  "savepoint_path": "/var/lib/wire/jobs/job-a1b2c3d4/savepoints/sp-1"
}
```

---

### 3.6 Endpoint: `POST /api/v1/jobs/{job_id}/pause`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs/{job_id}/pause` |
| **Auth** | Bearer JWT |
| **Description** | Pause a running job. Triggers a savepoint and suspends all tasks. |

**Response (200 OK)**

```json
{
  "job_id": "job-a1b2c3d4",
  "status": "PAUSED",
  "savepoint_path": "/var/lib/wire/jobs/job-a1b2c3d4/savepoints/sp-2"
}
```

---

### 3.7 Endpoint: `POST /api/v1/jobs/{job_id}/resume`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs/{job_id}/resume` |
| **Auth** | Bearer JWT |
| **Description** | Resume a paused job from its savepoint. |

**Response (200 OK)**

```json
{
  "job_id": "job-a1b2c3d4",
  "status": "DEPLOYING"
}
```

---

### 3.8 Endpoint: `POST /api/v1/jobs/{job_id}/savepoints`

| Field | Value |
| -- | -- |
| **Method** | POST |
| **Path** | `/api/v1/jobs/{job_id}/savepoints` |
| **Auth** | Bearer JWT |
| **Description** | Trigger a savepoint for a running job |

**Response (202 Accepted)**

```json
{
  "savepoint_id": "sp-1",
  "job_id": "job-a1b2c3d4",
  "status": "IN_PROGRESS",
  "trigger_time": "2024-01-15T12:00:00Z"
}
```

---

### 3.9 Endpoint: `GET /api/v1/jobs/{job_id}/savepoints/{savepoint_id}`

**Response (200 OK)**

```json
{
  "savepoint_id": "sp-1",
  "status": "COMPLETED",
  "path": "/var/lib/wire/jobs/job-a1b2c3d4/savepoints/sp-1",
  "trigger_time": "2024-01-15T12:00:00Z",
  "completion_time": "2024-01-15T12:00:01Z"
}
```

---

### 3.10 Endpoint: `GET /api/v1/jobs/{job_id}/savepoints`

Lists all savepoints for a job. Returns array of savepoint objects.

---

### 3.11 Endpoint: `DELETE /api/v1/jobs/{job_id}/savepoints/{savepoint_id}`

Deletes savepoint data from durable storage. Returns `204 No Content`.

---

### 3.12 Endpoint: `GET /api/v1/cluster`

| Field | Value |
| -- | -- |
| **Method** | GET |
| **Path** | `/api/v1/cluster` |
| **Auth** | Bearer JWT |
| **Description** | Get cluster status and node information |

**Response (200 OK)**

```json
{
  "leader": "node-1",
  "nodes": [
    {
      "node_id": "node-1",
      "address": "node1:4002",
      "status": "ALIVE",
      "role": "LEADER",
      "task_slots_total": 8,
      "task_slots_available": 3,
      "uptime": "72h15m30s"
    }
  ]
}
```

---

### 3.13 Endpoint: `DELETE /api/v1/cluster/nodes/{node_id}`

Removes a node from the Coordinator cluster. Running tasks are rescheduled.

---

### 3.14 Health & Metrics Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /healthz` | Public | Returns 200 if healthy, 503 if not |
| `GET /readyz` | Public | Returns 200 if ready to accept traffic |
| `GET /metrics` | Public | Prometheus-formatted metrics (see operations.md) |

---

## 4. Data Model & Storage

### 4.1 Job Metadata Schema

Stored in Coordinator's metadata store (persisted in PebbleDB; see WIP-09):

| Field | Type | Description |
| -- | -- | -- |
| job_id | string | Unique identifier (UUID-based) |
| name | string | User-provided job name |
| status | enum | CREATED/DEPLOYING/RUNNING/PAUSED/FAILING/CANCELED/FINISHED |
| config | blob | Serialized pipeline configuration |
| job_graph | blob | Serialized optimized JobGraph |
| parallelism | int | Effective parallelism |
| created_at | timestamp | Creation time |
| started_at | timestamp | First RUNNING transition |
| finished_at | timestamp | Terminal state transition |
| restart_count | int | Number of restarts from failure |
| latest_checkpoint | int64 | Latest completed checkpoint ID |
| savepoints | []SavepointMeta | List of savepoint metadata |

### 4.2 Savepoint Metadata

| Field | Type | Description |
| -- | -- | -- |
| savepoint_id | string | Unique identifier |
| job_id | string | Parent job |
| status | enum | IN_PROGRESS/COMPLETED/FAILED |
| path | string | Durable storage path (e.g., `/var/lib/wire/jobs/.../savepoints/sp-1`) |
| trigger_time | timestamp | When triggered |
| completion_time | timestamp | When completed |

---

## 5. Design Decisions & Trade-offs

### Decision 1: Savepoint-based upgrade (not hot swap)

|  |  |
| -- | -- |
| **Context** | Users need to upgrade job logic without losing state. |
| **Options Considered** | (A) Hot swap (replace operators in-place), (B) Savepoint → Cancel → Resubmit |
| **Decision** | Option B: Savepoint-based |
| **Rationale** | Hot swap requires ABI compatibility between old and new operators. Far too complex for v1. Savepoint approach is simple, correct, and well-understood (Flink uses the same model). |
| **Trade-offs Accepted** | Brief downtime during upgrade (seconds to minutes depending on state size). |
| **Revisit Trigger** | If downtime-sensitive users demand sub-second upgrades. |

### Decision 2: REST API (not gRPC for external clients)

|  |  |
| -- | -- |
| **Context** | External clients (CLI, dashboards, CI/CD) need to interact with the Coordinator. |
| **Options Considered** | (A) REST/JSON, (B) gRPC, (C) Both |
| **Decision** | Option A: REST/JSON (gRPC for internal RPC only) |
| **Rationale** | REST is universally accessible (curl, browsers, any language). Internal RPC uses gRPC for performance (see WIP-07). Users interact via CLI which wraps REST. |
| **Trade-offs Accepted** | Slightly higher overhead for REST vs gRPC. Two API surfaces to maintain. |
| **Revisit Trigger** | If programmatic clients demand gRPC for performance. |

### Decision 3: Savepoints are user-managed (not auto-GC'd)

|  |  |
| -- | -- |
| **Context** | Savepoints and automatic checkpoints both produce snapshots, but serve different purposes. |
| **Options Considered** | (A) Treat savepoints like checkpoints (auto-GC), (B) Savepoints persist until explicitly deleted |
| **Decision** | Option B: Savepoints persist |
| **Rationale** | Savepoints are intentional user actions (upgrade, rollback point). Deleting them automatically would lose the user's escape hatch. Checkpoints are internal and can be GC'd. |
| **Trade-offs Accepted** | Savepoints consume storage until manually deleted. |
| **Revisit Trigger** | If users accumulate too many savepoints. Add optional TTL. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Savepoint triggered while checkpoint in progress | Queue savepoint after current checkpoint completes. Return 202 with IN_PROGRESS. | Brief delay | Low |
| 2 | Cancel during DEPLOYING (state restore in progress) | Abort state download. Cancel tasks. Job → CANCELED. | Clean cancel | Low |
| 3 | Resume a non-PAUSED job | Return 409 CONFLICT: "Job is not paused" | No effect | Low |
| 4 | Submit job with savepoint from different job graph | Validate operator IDs match. If mismatch, reject with 400: "Savepoint incompatible with job graph" | Job rejected | Medium |
| 5 | Coordinator crashes during savepoint | On restart, savepoint status stays IN_PROGRESS. User can re-trigger. Old incomplete savepoint cleaned up by GC. | Savepoint lost | Medium |
| 6 | All workers die during RUNNING | Job → FAILING. Coordinator waits for workers to rejoin. After restart budget exhausted → CANCELED. | Downtime | High |
| 7 | Network partition between coordinator and workers | Workers self-terminate after losing heartbeat. Coordinator detects loss → FAILING → recovery. | Brief downtime | High |

---

## 7. Security & Compliance

### 7.1 Authentication & Authorization

* All REST API endpoints (except `/healthz`, `/readyz`, `/metrics`) require authentication.
* Auth mechanism defined in WIP-17 (Bearer JWT or API key).
* Job management operations require appropriate role (e.g., `admin` or `operator`).

### 7.2 Data Protection

* Job configurations may contain connector credentials via `${ENV_VAR}` references — the resolved values are never stored in job metadata or returned in API responses.
* Savepoint data inherits storage-level encryption (filesystem or volume encryption).

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | State machine transitions, API handler logic | Go `testing` | 100% of state transitions |
| Integration Tests | Full job lifecycle via REST API | httptest + MiniCluster | Submit → Run → Savepoint → Cancel |
| E2E Tests | CLI → REST → Coordinator → Workers | Docker Compose | Happy path + failure recovery |

### 8.1 Key Test Scenarios

1. Submit YAML job → verify CREATED → auto-transitions to RUNNING
2. Trigger savepoint → verify COMPLETED → cancel → resubmit from savepoint → verify state restored
3. Kill a worker during RUNNING → verify FAILING → auto-recovery → RUNNING
4. Exhaust restart budget → verify CANCELED
5. Pause → Resume → verify seamless continuation
6. Submit incompatible savepoint → verify 400 rejection
7. Concurrent savepoint + checkpoint → verify no conflict

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should the REST API support WebSocket for real-time job status streaming? | Tarun | Open |
| 2 | Should job submission be synchronous (wait for RUNNING) or async (return CREATED immediately)? | Tarun | Open |
| 3 | How are jobs persisted across Coordinator restarts? Raft log? Separate metadata DB? | Tarun | Open |
| 4 | Should there be a `/api/v1/jobs/{id}/rescale` endpoint for changing parallelism? | Tarun | Open |
| 5 | Risk: Savepoint storage costs could grow unbounded if users don't clean up. Consider auto-TTL. | — | Acknowledged |
