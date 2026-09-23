# Run a local cluster from configuration

Build from the repository root with Go 1.25 or newer:

```sh
go build -o wire ./cmd
```

The two checked-in node examples load and validate in tests. They use ports
4001 (HTTP), 4002 (coordinator RPC), and 4003 (worker data). In separate terminals:

```sh
./wire --config docs/examples/coordinator.yaml --metrics-addr 127.0.0.1:9090
```

```sh
./wire --config docs/examples/worker.yaml --metrics-addr 127.0.0.1:9091
```

The coordinator stores metadata under `data/coordinator`. The worker has four
slots. Each process needs a different metrics port. Verify startup:

```sh
curl --fail http://localhost:4001/readyz
./wire cluster status --coordinator http://localhost:4001
```

Expect readiness and one registered worker with four slots. No jobs are submitted
by node configuration. Use [the job CLI](job-cli.md) for encoded graph submission,
or [the SDK examples](../examples/) for programmatic jobs. Stop worker then coordinator
with Ctrl-C. Keep the coordinator data directory to retain metadata.

To add a second worker, reuse the file with explicit overrides:

```sh
./wire --config docs/examples/worker.yaml --worker-id second \
  --worker-listen 127.0.0.1:4004 --metrics-addr 127.0.0.1:9092
```

To use environment configuration, for example:

```sh
WIRE_WORKER_TASK_SLOTS=8 ./wire --config docs/examples/worker.yaml \
  --metrics-addr 127.0.0.1:9091
```

This is a local development cluster, not an HA/security deployment recipe.
Replica checkpoint storage requires its own existing directories and listener
per worker; see [checkpoint operations](operations.md). The HTTP API is plain
HTTP without authorization on this base. Bind local addresses or place it behind
an authenticated gateway before exposing it outside a trusted environment.

[The pipeline example](examples/pipeline.yaml) is a separate `wire/v1` document.
Register `app-input` and `app-output` factories and pass its bytes to
`sdk.ParsePipelineYAML`; `TestDocumentedPipelineExample` executes this exact file
with test factories. `wire --config` does not accept pipeline documents and
`wire jobs submit` currently takes an encoded JobGraph envelope, not this YAML.
[WIP-19](trds/WIP-19/README.md) tracks full declarative deployment and reload.
