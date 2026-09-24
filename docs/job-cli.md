# Job-management CLI

The `wire` binary can call the coordinator REST API without starting a node:

```bash
wire jobs list --coordinator http://localhost:4001
wire jobs list --status RUNNING
wire jobs get JOB_ID
wire jobs submit --file submission.json
wire jobs cancel JOB_ID
wire cluster status
wire cluster remove NODE_ID
wire savepoints list JOB_ID
wire savepoints get JOB_ID SAVEPOINT_ID
wire savepoints trigger JOB_ID
wire savepoints delete JOB_ID SAVEPOINT_ID
```

All commands accept `--coordinator` (default `http://localhost:4001`) and
`--timeout` (default `30s`). Flags may follow positional arguments. `--help`
lists commands and options. Responses are JSON on stdout; errors go to stderr
and exit nonzero. Requests honor SIGINT/SIGTERM, do not retry, and do not follow
redirects. Request and response bodies are limited to 4 MiB.

Submission takes the REST API JSON envelope, not a YAML pipeline or binary:

```json
{"name":"example","parallelism":1,"graph_bytes":"BASE64_MSGPACK_JOB_GRAPH"}
```

Generate the envelope with the public SDK's `env.ExportSubmission("job-name")`.
All operators must use named classes registered on workers; anonymous closures
cannot be exported. The method validates without starting a job or opening a
connector and leaves `Execute` available. The placeholder above is not runnable
graph data. A runnable example is:

```bash
go run ./sdk/examples/registered-worker -mode export > submission.json
wire jobs submit --file submission.json
```

Start the coordinator and example worker first as described in the
[SDK walkthrough](sdk-walkthrough.md#registering-operators-in-an-application-worker). A successful submission reports
acceptance, not successful deployment or completion. The server still accepts
legacy `config` bytes, but the scheduler does not interpret those as a graph.

Pause and resume use the durable workflow below. Triggering a savepoint reports
acceptance rather than a completed archive; poll its status before relying on it.
Cross-job upgrades, binary submission and CLI rescale support remain WIP-15 work.

### Cancellation completion

`wire jobs cancel JOB_ID` records the request and returns `CANCELING`. It does
not claim that worker tasks have already stopped. Use `wire jobs get JOB_ID`
until the status becomes `CANCELED`.

The coordinator aborts any active checkpoint before issuing cancellation,
retries commands for tasks that have not stopped, and keeps the job name
reserved during teardown. A task may need time to finish its connector's
`Close`. For an unreachable worker, cancellation waits for its execution lease
to expire; coordinator recovery also respects the old epoch's fencing interval.
Cancellation resumes after a coordinator restart. Repeating the command while
`CANCELING` is safe. Created, deploying, running, finishing, failing and paused
jobs can be canceled; terminal jobs reject the transition.

Cancellation without a savepoint does not create a new restore point. The
savepoint-before-cancel form is:

```bash
wire jobs cancel JOB_ID --savepoint
```

This requires a `RUNNING` job and returns HTTP 202 with `job` and `savepoint`
objects. Keep the returned savepoint ID. The job continues processing while the
snapshot queues or runs, then enters `CANCELING` only after the savepoint is
complete. `CANCELED` still waits for task teardown. Inspect the saved boundary
with `wire savepoints get JOB_ID SAVEPOINT_ID`; `job.savepoint_path` identifies
its checkpoint. The shared stop workflow exposes the pending ID in
`pause_savepoint_id` and sets `cancel_after_savepoint: true`.

A failed snapshot leaves the job running and records the failure in
`pause_failure`; it does not silently fall back to cancellation without state.
Repeating the pending request returns the same ID. A competing pause request is
rejected. Plain `jobs cancel` can override the pending request when immediate
cancellation is desired. Cancel-with-savepoint is a snapshot followed by stopping,
not a source drain: records after the boundary may be replayed on restore.
The completed checkpoint records a durable transaction decision, not an
acknowledgement from an external sink; restoration reconciles uncertain commits
using the transactional sink's idempotent recovery contract.

### Savepoints queued behind a checkpoint

`wire savepoints trigger JOB_ID` returns `IN_PROGRESS` with `queued: true` if a
checkpoint or earlier savepoint owns the job's snapshot boundary. Keep the
returned savepoint ID and poll `wire savepoints get JOB_ID SAVEPOINT_ID`.
The same record becomes active and then `COMPLETED` or `FAILED`; accepting the
request does not mean its archive is ready.

Queued requests are persisted, dispatched in arrival order, and take priority
over new automatic checkpoints. Unstarted requests survive coordinator recovery;
an active snapshot interrupted by recovery is still failed. Deleting a queued
request cancels it. Once its snapshot starts, deletion waits for the decision.
Canceling the job fails its remaining queued requests. Pause uses the same durable queue, as described below.

### Pause and resume from a savepoint

```sh
wire jobs pause JOB_ID
wire jobs get JOB_ID
# Poll until PAUSED, then:
wire jobs resume JOB_ID
wire jobs get JOB_ID
```

Pause returns HTTP 202 with the job and a queued savepoint ID. While that
savepoint waits or runs, the job remains `RUNNING` and exposes
`pause_savepoint_id`. Once the snapshot completes durably, the job enters
`PAUSING`, stops the old tasks, then enters `PAUSED`. A failed snapshot leaves
the job unpaused and exposes `pause_failure`; it does not consume the checkpoint
failure budget. An interrupted active snapshot fails during coordinator recovery;
a queued request survives and waits for a running job.

Resume returns `RESUMING` while waiting for worker capacity. The coordinator
redeploys from the exact pause checkpoint with a new deployment generation; this
manual deployment does not consume a failure-recovery attempt. Sources restore
saved offsets and managed operators restore their state. Ordinary sinks retain
their replay semantics; transactional sinks must implement the WIP-10 fencing
and idempotent commit contract. Records processed after the savepoint boundary
may be replayed after resume.

The pause savepoint cannot be deleted while it is needed for resume/recovery.
A newer completed checkpoint releases the pin; an invalid pinned checkpoint
returns an error rather than silently replaying from an older boundary.
Cancel remains available during `PAUSING`, `PAUSED` and `RESUMING`.

Upgrade coordinators before using these new lifecycle states; older coordinators
do not understand persisted `PAUSING`/`RESUMING` values. Legacy metadata-only
PAUSED jobs with no pinned checkpoint are rejected on resume rather than restarted
from empty state.

### Node removal

`wire cluster remove NODE_ID` durably revokes a worker's admission. The response
acknowledges the request; affected jobs recover only after their old tasks stop
or their authority expires. `wire cluster status` retains a REMOVED entry.
Restart policy and checkpoint availability still determine recovery. Use a new
worker ID for a replacement; a removed identity cannot re-register.

### Upgrade from a savepoint

Use the completed checkpoint path returned by the coordinator, rather than a
worker filesystem path. Keep the operator IDs, ordering, parallelism and state
serializers compatible in the new submission. Register the replacement operator
classes on workers before submitting their graph.

```bash
wire jobs cancel OLD_JOB_ID --savepoint
wire jobs get OLD_JOB_ID
# Wait for CANCELED; retain savepoint_path from the response.
wire jobs submit --file upgraded-submission.json --savepoint jobs/OLD_JOB_ID/checkpoints/1
wire jobs get NEW_JOB_ID
```

For `jobs submit`, `--savepoint` takes a path; for `jobs cancel` it is a boolean.
Put `jobs submit` before its flags. The submit flag sets the JSON envelope's
`savepoint` field, overriding that field if already present in the file. REST
clients can provide the field directly. Submission rejects unknown fields,
multiple JSON values, and simultaneous `config` and `graph_bytes` fields.

The predecessor must be stopped and the savepoint must be its latest completed
checkpoint. A successful HTTP 201 means a successor was durably accepted, not
that restore has finished. The response exposes `restore_savepoint_path` while
the new job still depends on that boundary. That reference blocks savepoint
deletion and clears after the successor completes its own checkpoint.

Only one successor can be accepted from a predecessor. Concurrent or repeated
submissions cannot create two writers for the same transaction lineage. For
another upgrade, take a new savepoint from the current successor, stop it, and
submit from that boundary. A predecessor with an accepted successor cannot be
reused, even if that successor later fails; automatic retries use the successor's
configured recovery policy. Operator state-format migration and redistribution
of opaque state are not inferred from a changed graph.

The upgraded job gets a new runtime ID, but transactional sinks retain their
original external job/task identity with a higher deployment generation.
Checkpoint IDs continue above the predecessor's high-water mark. This preserves
the idempotent commit/recovery contract when a prior commit reply was lost.
