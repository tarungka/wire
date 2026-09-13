# Job-management CLI

The `wire` binary can call the coordinator REST API without starting a node:

```bash
wire jobs list --coordinator http://localhost:4001
wire jobs list --status RUNNING
wire jobs get JOB_ID
wire jobs submit --file submission.json
wire jobs cancel JOB_ID
wire cluster status
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

Use an encoded `rpc.JobGraph` with registered worker operator factories. The
placeholder above is not runnable graph data. A successful submission reports
acceptance, not successful deployment or completion. The server still accepts
legacy `config` bytes, but the scheduler does not interpret those as a graph.

`wire jobs pause JOB_ID` and `wire jobs resume JOB_ID` expose the existing REST
endpoints. Those endpoints currently update metadata; they do not provide a
completed runtime pause/savepoint/restore workflow. Similarly, triggering a
savepoint returns its current metadata status, not a guarantee of a completed
durable snapshot. Inspect returned status before relying on it. Upgrades,
rescaling, and binary submission remain unsupported.
