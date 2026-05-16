# Usage

**Status:** Canon
**Version:** 1.0.0
**Context:** Getting Started & API Reference

---

> **Current implementation boundary:** Cluster mode currently supports
> forward-only named source/map/flatmap/filter/sink chains. `KeyBy`, windows,
> process/reduce operators, durable engine Pebble state, real savepoint restore,
> and exactly-once cluster recovery are not wired end to end yet. See
> [current-capabilities.md](current-capabilities.md).

## 1. Prerequisites

* Go 1.21 or later
* `make`
* `jq` (optional, for pretty-printing JSON responses)

## 2. Building

```bash
make build
```

This produces the `wire` binary in the project root.

## 3. Running the Coordinator

```bash
./wire \
  --mode coordinator \
  --http-listen :4001 \
  --listen :4002 \
  --election-backend noop \
  --coordinator-data-dir data/coordinator \
  --debug
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | | Node mode: `coordinator` or `worker` |
| `--http-listen` | `:4001` | HTTP API listen address |
| `--listen` | `:4002` | Wire protocol listen address |
| `--coordinator-data-dir` | `data/coordinator` | Coordinator metadata storage directory |
| `--node-id` | hostname | Coordinator node ID |
| `--election-backend` | `noop` | Leader election backend: `noop` (single-node) or `filelock` |
| `--election-lock-path` | `data/coordinator/leader.lock` | File path for the filelock election backend |
| `--config` | `.config/config.json` | Path to one or more config files (merged in order) |
| `--debug` | `false` | Enable verbose debug logging |
| `--max-frame-size` | `16777216` | Max wire protocol frame size in bytes |

### TLS Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--node-cert` | | TLS certificate file path |
| `--node-key` | | TLS private key file path |
| `--node-ca` | | CA certificate for peer verification |
| `--node-verify-client` | `false` | Require mutual TLS |

## 3b. Running a Worker

Workers connect to the coordinator, register, and receive task deployments via heartbeat.

```bash
./wire \
  --mode worker \
  --coordinator-addr localhost:4002 \
  --task-slots 4 \
  --debug
```

### Worker Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | | Must be `worker` |
| `--coordinator-addr` | | Address of the coordinator's wire protocol listener |
| `--worker-id` | hostname | Worker node ID |
| `--task-slots` | `4` | Number of concurrent task slots |
| `--debug` | `false` | Enable verbose debug logging |

### Task Deployment Flow

1. Submit a job via the HTTP API — job enters `CREATED` state
2. The coordinator scheduler (runs every 2s) picks up `CREATED` jobs
3. Scheduler generates task descriptors, assigns tasks to workers with available slots, transitions job to `DEPLOYING`
4. Workers receive `DeployTask` commands in the next heartbeat response
5. Workers process the command and send `UpdateTaskStatus(RUNNING)` back to the coordinator
6. When all tasks report `RUNNING`, the coordinator transitions the job to `RUNNING`

### Example End-to-End

```bash
# Terminal 1: coordinator
./wire --mode coordinator --listen :4002 --http-listen :4001 \
  --coordinator-data-dir ./data/coordinator/ --election-backend noop --debug

# Terminal 2: worker
./wire --mode worker --coordinator-addr localhost:4002 --task-slots 4 --debug

# Terminal 3: submit and observe
curl -s -X POST localhost:4001/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-pipeline","parallelism":2,"config":"test"}' | jq

# Wait ~7s (scheduler tick + heartbeat), then check status:
curl -s localhost:4001/api/v1/jobs | jq
# Expected: job status is "RUNNING"

# Cancel the job:
curl -s -X POST localhost:4001/api/v1/jobs/{job_id}/cancel | jq
```

## 4. Configuration

Instead of flags, you can use a YAML or JSON config file via `--config`:

```bash
./wire --config .config/config.yaml
```

See [`.config/config.yaml`](../.config/config.yaml) for an example that defines sources (Mon

## 5. Health & Readiness

```bash
# Liveness probe
curl http://localhost:4001/healthz

# Readiness probe
curl http://localhost:4001/readyz

# Current leader info
curl http://localhost:4001/api/v1/cluster/leader
```

## 6. Job Management API

### Submit a Job

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-pipeline",
    "parallelism": 4,
    "config": "{\"sources\":[],\"sinks\":[]}"
  }' | jq
```

Response:

```json
{
  "id": "job_abc123",
  "name": "my-pipeline",
  "status": "CREATED",
  "parallelism": 4,
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-01T00:00:00Z"
}
```

### List Jobs

```bash
# All jobs
curl -s http://localhost:4001/api/v1/jobs | jq

# Filter by status
curl -s 'http://localhost:4001/api/v1/jobs?status=RUNNING' | jq
```

Response:

```json
{
  "jobs": [
    {
      "id": "job_abc123",
      "name": "my-pipeline",
      "status": "RUNNING",
      "parallelism": 4,
      "created_at": "2025-01-01T00:00:00Z",
      "updated_at": "2025-01-01T00:00:01Z"
    }
  ]
}
```

### Get a Job

```bash
curl -s http://localhost:4001/api/v1/jobs/{job_id} | jq
```

Response:

```json
{
  "id": "job_abc123",
  "name": "my-pipeline",
  "status": "RUNNING",
  "parallelism": 4,
  "created_at": "2025-01-01T00:00:00Z",
  "updated_at": "2025-01-01T00:00:01Z",
  "started_at": "2025-01-01T00:00:01Z",
  "restart_count": 0,
  "latest_checkpoint": 5
}
```

### Cancel a Job

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/cancel | jq
```

### Pause a Job

Pausing a job currently creates savepoint metadata before suspending execution.
It does not yet create a restorable state snapshot.

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/pause | jq
```

Response:

```json
{
  "job": {
    "id": "job_abc123",
    "name": "my-pipeline",
    "status": "PAUSED",
    "parallelism": 4,
    "created_at": "2025-01-01T00:00:00Z",
    "updated_at": "2025-01-01T00:00:05Z",
    "started_at": "2025-01-01T00:00:01Z",
    "restart_count": 0,
    "latest_checkpoint": 10
  },
  "savepoint": {
    "id": "sp_xyz789",
    "job_id": "job_abc123",
    "status": "IN_PROGRESS",
    "trigger_time": "2025-01-01T00:00:05Z"
  }
}
```

### Resume a Job

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/resume | jq
```

## 7. Savepoint API

### Trigger a Savepoint

This endpoint currently records savepoint metadata only. Barrier injection,
state snapshotting, and restore from savepoint are target capabilities.

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/savepoints | jq
```

### List Savepoints

```bash
curl -s http://localhost:4001/api/v1/jobs/{job_id}/savepoints | jq
```

### Get a Savepoint

```bash
curl -s http://localhost:4001/api/v1/jobs/{job_id}/savepoints/{savepoint_id} | jq
```

### Delete a Savepoint

```bash
curl -s -X DELETE http://localhost:4001/api/v1/jobs/{job_id}/savepoints/{savepoint_id} | jq
```

## 8. Cluster API

### Cluster Status

```bash
curl -s http://localhost:4001/api/v1/cluster | jq
```

Response:

```json
{
  "leader": {
    "leader_id": "node-1",
    "leader_http_addr": ":4001",
    "leader_epoch": 1,
    "is_self": true
  },
  "workers": [
    {
      "id": "worker-1",
      "address": "10.0.0.2:4002",
      "task_slots_total": 8,
      "task_slots_available": 4,
      "last_heartbeat": "2025-01-01T00:00:10Z",
      "running_tasks": ["task_001", "task_002"]
    }
  ]
}
```

### Remove a Node

```bash
curl -s -X DELETE http://localhost:4001/api/v1/cluster/nodes/{node_id} | jq
```

## 9. Job Lifecycle

Jobs follow this state machine:

```
CREATED -> DEPLOYING -> RUNNING -> FINISHING -> FINISHED
   |          |            |          |
   |          |            |          +-> FAILING -> FAILED
   |          |            |
   |          |            +-> CANCELING -> CANCELED
   |          |            |
   |          |            +-> PAUSED -> (DEPLOYING, resumes)
   |          |
   |          +-> CANCELING -> CANCELED
   |          |
   |          +-> FAILING -> FAILED
   |                    |
   |                    +-> DEPLOYING (restart)
   |
   +-> CANCELING -> CANCELED
```

* **CREATED** — Job submitted but not yet deployed; scheduler picks it up
* **DEPLOYING** — Scheduler assigned tasks to workers; awaiting all tasks to report running
* **RUNNING** — Actively processing data
* **FINISHING** — Draining; completing gracefully
* **FINISHED** — Completed successfully (terminal)
* **FAILING** — Error encountered, shutting down
* **FAILED** — Terminated due to error (terminal)
* **CANCELING** — Cancellation requested
* **CANCELED** — Canceled by user (terminal)
* **PAUSED** — Suspended with savepoint taken

Terminal states: `FINISHED`, `FAILED`, `CANCELED`.

## 10. SDK Quick Start

Wire includes an embedded SDK for building pipelines in Go:

```go
package main

import (
    "context"
    "fmt"

    "github.com/tarungka/wire/sdk"
)

func main() {
    env := sdk.New()

    sink := &sdk.CollectSink{}

    env.AddSource(&sdk.SliceSource{
        Events: []sdk.Event{
            {Value: []byte("hello")},
            {Value: []byte("world")},
        },
    }).
        Map(func(e sdk.Event) (sdk.Event, error) {
            e.Value = append(e.Value, '!')
            return e, nil
        }).
        Filter(func(e sdk.Event) (bool, error) {
            return len(e.Value) > 0, nil
        }).
        AddSink(sink)

    result, err := env.Execute(context.Background())
    if err != nil {
        panic(err)
    }
    fmt.Printf("Processed %d records in %s\n",
        result.Metrics.RecordsOut, result.Metrics.Duration)
}
```

Key SDK types:

* `StreamExecutionEnvironment` — entry point; create with `sdk.New()`
* `DataStream` — returned by `AddSource()`; chain `Map`, `FlatMap`, `Filter`, `KeyBy`, `Union`, `AddSink`
* `KeyedStream` — returned by `KeyBy()`; embedded/local API for keyed operations. Cluster mode does not support keyed shuffle yet.
* `JobResult` — returned by `Execute()`; contains `JobID`, `Err`, and `Metrics`

## 11. Error Responses

All errors follow a standard format:

```json
{
  "error": "ERROR_CODE",
  "message": "Human-readable description"
}
```

### Common Error Codes

| Code | HTTP Status | Description |
|------|-------------|-------------|
| `JOB_NOT_FOUND` | 404 | Job ID does not exist |
| `JOB_EXISTS` | 409 | A job with this name already exists |
| `INVALID_TRANSITION` | 409 | Illegal state transition for the job's current status |
| `JOB_NOT_RUNNING` | 409 | Operation requires the job to be in RUNNING state |
| `JOB_NOT_PAUSED` | 409 | Operation requires the job to be in PAUSED state |
| `INVALID_CONFIG` | 400 | Job configuration is invalid |
| `INVALID_REQUEST` | 400 | Malformed JSON body |
| `INVALID_STATUS` | 400 | Unknown job status filter value |
| `SAVEPOINT_NOT_FOUND` | 404 | Savepoint ID does not exist |
| `NODE_NOT_FOUND` | 404 | Worker node ID does not exist |
| `NOT_LEADER` | 503 | This node is not the leader; retry against the leader |
| `NO_LEADER` | 503 | No leader has been elected yet |
| `NOT_IMPLEMENTED` | 501 | Feature not yet supported |
| `INTERNAL_ERROR` | 500 | Unexpected server error |
