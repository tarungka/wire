# Wire examples

Two small programs that show how the Phase 1 worker/SDK split works in
practice.

| Program | Purpose |
|---------|---------|
| [`wire-worker-example`](wire-worker-example/) | A custom worker binary that registers operators and connects to a coordinator. |
| [`submit-uppercase-job`](submit-uppercase-job/) | An SDK client that builds a `Source → Map → Sink` graph and submits it in Cluster mode. |

The architectural split is deliberate: user-defined functions can't be
serialized across an RPC boundary, so workers run a binary that imports
the user's UDFs and registers them by name (see
[WIP-16](../docs/trds/WIP-16/README.md)).

## End-to-end smoke test

This walkthrough boots one coordinator and one worker on the same machine,
then submits a job. All three commands run in separate terminals.

### Terminal 1 — coordinator

```bash
make build
./wire \
    --mode coordinator \
    --http-listen 127.0.0.1:4001 \
    --listen 127.0.0.1:4002 \
    --election-backend noop \
    --coordinator-data-dir data/coordinator
```

### Terminal 2 — worker

```bash
go run ./examples/wire-worker-example \
    --coordinator-addr 127.0.0.1:4002 \
    --task-slots 4
```

You should see the worker register and start the heartbeat loop.

### Terminal 3 — submit a job

```bash
go run ./examples/submit-uppercase-job \
    --coordinator-url http://127.0.0.1:4001 \
    --message hello --message world --message wire
```

The submitter exits when the job reaches `FINISHED`. Check the worker's
output for processing logs. You can also query the coordinator directly:

```bash
curl -s http://127.0.0.1:4001/api/v1/jobs | jq
curl -s http://127.0.0.1:4001/api/v1/jobs/<job_id> | jq
```

## Cross-process output capture

The `memory-sink` connector stores collected events in a package-level map
inside the *worker* process — fine for the in-process integration test
(`internal/worker/integration_test.go`) but not visible to a separate
submitter binary. To assert sink output in a real distributed test, use a
sink that writes to a shared store (file, S3, Postgres) and read it back
from the test.

## What about plain `curl`?

You can submit a job with `curl`, but the request body needs a base64-encoded
msgpack `JobGraph`. The submitter binary above uses the SDK to build that
payload — the same encoder you would call from your own application code.
For ad-hoc inspection, prefer the SDK path; the wire format is internal and
will evolve as the WIPs land.
