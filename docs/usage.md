# Usage

**Status:** Canon
**Version:** 1.0.0
**Context:** Getting Started & API Reference

---

## 1. Prerequisites

* Go 1.25.0 or later
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
| `--mode` | `coordinator` | Node mode: `coordinator` or `worker` |
| `--http-listen` | `:4001` | HTTP API listen address |
| `--listen` | `:4002` | Wire protocol listen address |
| `--coordinator-data-dir` | `data/coordinator` | Coordinator metadata storage directory |
| `--node-id` | hostname | Coordinator node ID |
| `--election-backend` | `noop` | Leader election backend: `noop` (single-node), `filelock` (same-host HA), or `kubernetes` (Lease election) |
| `--election-lock-path` | `data/coordinator/leader.lock` | File path for the filelock election backend |
| `--config` | `.config/config.json` | Path to one or more config files (merged in order) |
| `--debug` | `false` | Enable verbose debug logging |
| `--max-frame-size` | `16777216` | Max wire protocol frame size in bytes |

### TLS Flags

Node TLS flags configure coordinator-worker RPC connections (TLS 1.3 minimum). For mTLS, configure the coordinator certificate/key and CA with `--node-verify-client`, and give each worker a client certificate whose Common Name matches its worker ID. Workers verify the coordinator hostname or `--node-verify-server-name` override. These flags do not secure HTTP, data streams or checkpoint replica transfers; see [WIP-07's runtime contract](trds/WIP-07/runtime-contract.md#tls-and-identity).


| Flag | Default | Description |
|------|---------|-------------|
| `--node-cert` | | TLS certificate file path |
| `--node-key` | | TLS private key file path |
| `--node-ca` | | CA certificate for peer verification |
| `--node-verify-client` | `false` | Require mutual TLS |

## 3b. Running a Worker

Workers connect to the coordinator, register, and open a `WatchCommands` stream for pushed task deployments. Heartbeats provide liveness and a fallback command-delivery path.

```bash
./wire \
  --mode worker \
  --coordinator-addr localhost:4002 \
  --task-slots 4 \
  --metrics-addr :9091 \
  --debug
```

The metrics override avoids conflicting with the coordinator on the same host.

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
4. Workers receive `DeployTask` commands through `WatchCommands`, with heartbeat delivery as a fallback
5. Workers process the command and send `UpdateTaskStatus(RUNNING)` back to the coordinator
6. When all tasks report `RUNNING`, the coordinator transitions the job to `RUNNING`

### Starting a cluster and submitting work

Start the coordinator and worker with the commands above. Cluster jobs require
an encoded graph and matching operator factories registered in the worker.
The SDK cluster executor submits `graph_bytes` automatically; Go function
closures cannot be shipped to another process. Use named operators and register
their implementations in the worker application.

For an existing JSON graph submission envelope:

```bash
./wire jobs submit --file submission.json --coordinator http://localhost:4001
./wire jobs list --coordinator http://localhost:4001
./wire jobs cancel JOB_ID --coordinator http://localhost:4001
```

See [job CLI](job-cli.md) for the envelope and command reference. Submission
acceptance does not imply deployment success; inspect the job status. Arbitrary
`config` strings such as `"test"` are accepted by the legacy HTTP field but fail
when the scheduler decodes the graph. For a runnable local pipeline, use the
embedded SDK example below.

## 4. Configuration

Instead of flags, you can use a YAML or JSON config file via `--config`:

```bash
./wire --config .config/config.yaml
```

See the generated [configuration reference](configuration-reference.md) for fields, defaults, and CLI mappings, and [configuration validation](configuration-validation.md) for runtime limits. Node configuration is separate from the [YAML pipeline format](../sdk/pipeline_yaml.md).

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

The JSON request accepts `name`, `parallelism`, and `graph_bytes` (a base64-encoded
msgpack `rpc.JobGraph`). The SDK cluster executor creates this envelope. Operators
must name factories available on the workers; an embedded function closure is
not a distributed operator implementation.

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs \
  -H 'Content-Type: application/json' \
  --data-binary @submission.json | jq
```

`submission.json` has this shape; the graph value is a placeholder, not runnable data:

```json
{"name":"my-pipeline","parallelism":4,"graph_bytes":"BASE64_MSGPACK_JOB_GRAPH"}
```

A successful request returns HTTP 201 and the job metadata with status `CREATED`.
Poll the job endpoint to observe deployment and errors. The legacy `config`
field does not parse a YAML pipeline or a JSON sources/sinks configuration.

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

### Pause and resume limitations

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/pause | jq
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/resume | jq
```

Pause triggers a checkpoint-backed savepoint and changes job metadata to
`PAUSED`, but does not wait for savepoint completion or implement a task
suspension protocol. Resume changes metadata to `DEPLOYING`; redeployment from
the savepoint is still unimplemented. These endpoints are not a completed
stop-and-restore workflow. The pause response contains `job` and `savepoint`;
do not assume the returned savepoint has status `COMPLETED`.

## 7. Savepoint API

### Trigger a Savepoint

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/savepoints | jq
```

Savepoint creation is asynchronous. Poll the get endpoint until status is
`COMPLETED` before using the snapshot; handle failure explicitly.

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

### Rescale a job

After a savepoint completes, request a stop-start rescale:

```bash
curl -s -X POST http://localhost:4001/api/v1/jobs/{job_id}/rescale \
  -H 'Content-Type: application/json' \
  -d '{"savepoint_id":"SAVEPOINT_ID","operators":{"map-operator":8}}' | jq
```

Replace the IDs and operator name with values from your job. Use either
`operators` or global `parallelism`, never both. Global rescale preserves source,
sink, and Forward-connected boundary counts; an all-Forward graph cannot change
through global rescale. Follow the job status and `rescale_failure`, and read
[rescale safety](rescale-safety.md) for state redistribution and rollback limits.

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
* **PAUSED** — Metadata state; see pause/resume limitations above

Terminal states: `FINISHED`, `FAILED`, `CANCELED`.

## 10. SDK Quick Start

Wire includes an embedded SDK for building pipelines in Go. Save this as
`main.go` in a Go module that depends on Wire, then run `go run .`. It prints
`hello!` and `world!`. The source and sink below are application-defined; the
example does not provide durable replay or transactional output.

```go
package main

import (
	"context"
	"fmt"

	"github.com/tarungka/wire/sdk"
)

type sliceSource struct{ events []sdk.Event }

func (s *sliceSource) Open(context.Context) error { return nil }
func (s *sliceSource) ReadBatch(context.Context) ([]sdk.Event, error) {
	events := s.events
	s.events = nil
	return events, nil
}
func (s *sliceSource) Close() error { return nil }

// Required for interface compatibility; runtime strategies generate watermarks.
func (s *sliceSource) GenerateWatermark() int64 { return 0 }

type printSink struct{}

func (*printSink) Open(context.Context) error { return nil }
func (*printSink) Write(_ context.Context, e sdk.Event) error {
	fmt.Println(string(e.Value))
	return nil
}
func (*printSink) Close() error { return nil }

func main() {
	env := sdk.New()
	env.AddSource(&sliceSource{events: []sdk.Event{
		{Value: []byte("hello")}, {Value: []byte("world")},
	}}).
		Map(func(e sdk.Event) (sdk.Event, error) {
			e.Value = append(e.Value, '!')
			return e, nil
		}).
		AddSink(&printSink{})
	if _, err := env.Execute(context.Background()); err != nil {
		panic(err)
	}
}
```

Key SDK types:

* `StreamExecutionEnvironment` — entry point; create with `sdk.New()`
* `DataStream` — returned by `AddSource()`; chain `Map`, `FlatMap`, `Filter`, `KeyBy`, `Union`, `AddSink`
* `KeyedStream` — returned by `KeyBy()`; enables keyed state and windowing
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

For coordinator HA configuration and worker discovery, see the [WIP-09 runtime contract](trds/WIP-09/runtime-contract.md) and [Kubernetes deployment requirements](trds/WIP-09/kubernetes.md).
