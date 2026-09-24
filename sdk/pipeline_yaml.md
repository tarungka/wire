# YAML pipelines

`ParsePipelineYAML(data, connectors)` accepts one `wire/v1` Pipeline document,
validates it, and returns a `YAMLPipeline` backed by the same StreamGraph as the
Go SDK. Supply `PipelineConnectors.Sources` and `.Sinks` maps from type names to
configuration factories. Factories receive the YAML `config` map; they should
validate connector-specific options and return unopened SDK Source/Sink values.
The parser validates all references, expressions, and transform configuration
before calling factories. It does not discover or download connector types.

```yaml
apiVersion: wire/v1
kind: Pipeline
metadata:
  name: valid-orders
spec:
  sources:
    - {name: input, type: app-input}
  transforms:
    - name: parsed
      type: json-parse
      input: input
      config: {target-field: payload}
    - name: valid
      type: filter
      input: parsed
      config: {expression: "payload.status != 'invalid'"}
    - name: projected
      type: map
      input: valid
      config: {expression: "{'user': payload.user_id}"}
  sinks:
    - {name: output, type: app-output, input: projected}
```

For this example, register `app-input` and `app-output` in the supplied maps,
parse the document, then call `pipeline.Execute(ctx)`. This uses the metadata
name as the job name and the SDK's single-execution environment. Connector Open
and Close belong to runtime execution, not parsing.

## Transform semantics

- `json-parse`: parses the current event value and wraps it under `target-field`.
  The target must be an identifier and cannot replace reserved event variables.
- `filter`: a CEL expression must evaluate to bool.
- `map`: a CEL result becomes the event's JSON value; key/time/headers remain.
- `flat-map`: a CEL list produces one output event per element, each JSON-encoded.
- `key-by`: compiles a string/integer key selector and a Hash edge in the graph.
- `select`: projects dotted object paths. Output keys are the literal paths;
  for example `payload.id` produces `{"payload.id": ...}`. Missing paths fail.
- `rename`: simultaneously renames top-level fields using `mappings`. Duplicate
  targets, missing source fields, and overwriting untouched fields fail.
- Window transforms populate the SDK window assigner and count/sum/min/max
  aggregator. Positive durations are required. Window execution supports named
  late outputs and the selected managed state backend.

CEL variables are `key` (string), `value` (parsed JSON when valid, otherwise raw
string), `event_time` (integer), `headers` (string map), and declared JSON parse
targets such as `payload`. Arbitrary projected fields can be accessed through
`value`. Expressions are compiled once, with a 16,384-character expression limit,
100-level parser recursion limit, and 100,000-operation evaluation cost limit.
No host I/O functions are exposed. JSON integer values retain signed/unsigned
64-bit precision; integers outside that range are rejected by JSON parsing.
Expression results must be JSON-compatible with string map keys.

## Validation and execution limits

Unknown schema/transform fields, multiple YAML documents, duplicate names,
unavailable connector types, missing input references, cycles, invalid duration
settings, and invalid expressions fail parsing. Forward input references are
allowed. Connector-specific `config` validation is the factory's responsibility.

`Graph()` exposes the compiled SDK graph for integration. Linear pipelines
support parallel execution when every source and sink uses an instance-aware
factory. The legacy `Sources`/`Sinks` factories remain limited to parallelism
one. Graphs may contain multiple sources and fan-out branches; key-by can feed
ordinary transforms or sinks without a window. Each `input` names one upstream
operator; this schema does not yet expose a multi-input union/join field.

`PipelineConnectors.SourceInstances` and `.SinkInstances` map type names to
`func(map[string]any, InstanceContext) (Source, error)` and the corresponding
sink function. `InstanceContext` contains `Index` and `Parallelism`; use these
to partition source input instead of emitting the whole input from each copy.
Each call receives a deep copy of its YAML configuration and must return a
fresh, unopened connector. Instance factories run during execution after graph
validation. Registering both legacy and instance factories for the same type is
an error. Local named DLQ destinations require a legacy shared sink factory.

Checkpoint and restart settings use the SDK's local coordinator/worker runtime
and require instance-aware factories for every source and sink, including at
parallelism one. This permits a fresh connector on each deployment attempt;
sources must still implement the SDK checkpoint/restore contract for replay,
and exactly-once external output requires transactional sinks. Factory support
alone does not provide either guarantee. Checkpointed sources currently park at
EOF until all job sources exhaust. Mixed bounded/unbounded jobs therefore keep
the exhausted source task and its output streams open; independently finishing
those branches is an open lifecycle requirement. Task recovery acceptance covers CEL and both window backends; process replacement
and mixed-source completion remain in the completion audit.

There is no automatic reload, drain/switchover or topology migration yet. Invalid reload candidates can be
validated with ParsePipelineYAML, but callers must not infer a safe switchover
protocol from that API. WIP-19 remains Partially Implemented.

### Source watermarks

A source under `spec.sources` accepts `watermark` alongside `name`, `type`, and
`config`:

```yaml
watermark:
  strategy: bounded-ooo
  max_ooo: 5s
  emit_interval: 200ms
  idle_timeout: 1m
```

Strategies are `bounded-ooo`, `monotonic`, and `ingestion-time`. An omitted
strategy uses bounded out-of-orderness. Ingestion time replaces producer event
timestamps with the ingestion clock. `max_ooo` is only valid for bounded-ooo;
omitting it selects the five-second default. Explicit `0s` means zero
tolerance, equivalent to `monotonic`. Invalid strategies, durations, unknown fields, and watermark settings
on transforms or sinks are rejected before connector factories run.

## State backend selection

Pipeline YAML accepts the nested WIP-18 configuration under `spec`:

```yaml
state_backend:
  type: hashmap
  hashmap:
    max_memory_mb: 256
```

For Pebble, use `type: pebble` and optional `pebble.data_dir` and
`pebble.max_compaction_concurrency`. An omitted HashMap limit means 256 MiB;
explicit zero means unlimited. Negative limits, overflow, unknown backend
names and unknown nested fields are rejected before connector factories run.
The limit applies to logical state payload per managed operator instance.

`pipeline.SetStateBackend(sdk.NewHashMapStateBackend(64))` overrides the YAML
selection. An omitted `state_backend` preserves the environment default;
embedded Pebble uses temporary storage unless a directory is configured.
The connector and deployment requirements above still apply. Full CLI/pipeline/system precedence and distributed YAML execution are
tracked in the [WIP-19 completion audit](../docs/trds/WIP-19/completion.md).

## Window values and projection

YAML windows emit JSON objects so downstream `select`, `filter`, `map` and
`rename` can consume their results. Every result contains `key`, `window_start`,
`window_end`, `is_update`, and a numeric field named after its aggregation:
`count`, `sum`, `min` or `max`. Window bounds are event-time milliseconds; the
event retains its key, window-end timestamp and SDK window metadata headers.
For example, the proposal's `fields: [key, count, window_start, window_end]`
projection can follow a count window directly. Late-output records retain their
original payload and do not become result objects.

Count accepts any payload and produces an unsigned integer. Sum/min/max accept
a JSON number as the entire input value; use an upstream map such as
`expression: "value.amount"` to select an object field. These numeric aggregates
use float64 arithmetic, including its rounding limits for large integers.
Invalid JSON, nonnumeric values and non-finite aggregate results return errors
without publishing partial window state. Count overflow is also rejected.

Compatibility: earlier YAML windows exposed the Go SDK's binary aggregate
bytes. YAML consumers must now read the named JSON aggregate field. Numeric
YAML input is JSON rather than binary float bytes. Ordinary Go SDK window
values and binary checkpoint accumulator formats are unchanged.

### Registered worker execution

Call `registry.RegisterPipelineTransforms()` once before starting workers. This
registers the ten YAML transform types under versioned `wire.yaml.v1.*` classes.
Workers independently validate and compile the serialized configuration; Go
closures from the submitting process are not deployed. Definitions are bounded
to 1 MiB and 1024 variable names, with the existing CEL parser limits applied.

Supply `PipelineConnectors.NamedSources` and `.NamedSinks` maps from YAML type
to application worker class, then call `pipeline.SetCoordinator(url).Execute(ctx)`.
Use `SetCoordinatorSecurity` for HTTPS and credentials. Every selected class must
be registered on every worker; each factory must create a fresh connector.
Connector config is JSON, including for a named `__dlq__` sink. Such sinks receive
the normal DLQ envelope with original event, error and operator attribution.
Named bindings are remote-only and cannot overlap local factories for a type.

For the public HTTP connector, call `httpworker.RegisterYAML(registry)` from
`sdk/connectors/httpapi/worker` and bind type `http-api` to worker class
`http-api.yaml.v1`. Its snake_case configuration matches `SourceConfig` and
`SinkConfig`; sink `timeout`, `initial_delay` and `max_delay` use duration strings
such as `30s`. Unknown fields are rejected. Registration does not open listeners
or send requests. The original `http-api` MessagePack class remains unchanged.

HTTP sources are unbounded and acknowledge in-memory acceptance, not durable
checkpoint completion. Their sequence offsets do not make client replay
automatic. HTTP sinks require receiver-side idempotency for replay-safe output.
For multiple source instances, assign distinct listen addresses through custom
worker factories; the shared YAML address is not partition-expanded.

### CLI submission

`wire jobs submit --file pipeline.yaml --format yaml --coordinator https://host:4001`
compiles and submits a YAML pipeline and prints the coordinator's response without
waiting for completion. The default format remains the existing REST JSON envelope.
The same `--ca-cert`, client certificate, API key/password-file and `--savepoint`
flags apply to either format. Both input and compiled request are limited to 4 MiB.
Malformed graphs and CEL expressions are rejected before the HTTP request.

The stock CLI maps `http-api` source and sink types to `http-api.yaml.v1`.
Stock node-mode workers install YAML transforms and HTTP YAML factories in a
private registry. Custom SDK workers must call `RegisterPipelineTransforms()`
and `httpworker.RegisterYAML(registry)` before starting. Custom applications can use `ParsePipelineYAML` and
`YAMLPipeline.ExportSubmission` with their own named bindings, then submit the
exported JSON through the existing CLI. Worker factories
validate connector configuration on deployment; compiling a named binding does
not open or validate the target connector's runtime resources.

### Watching validated candidates

`WatchPipelineFile` reads a bounded regular file and compiles the initial
pipeline plus stable content changes. It only accepts named-worker bindings,
so candidate validation cannot construct local connectors. Changed bytes must
match across two polls (250ms default), including atomic file replacements;
size and modification timestamps are not used as change identities. Invalid
edits are reported through `OnRejected` and never reach the apply callback.

The callback is serialized and an error stops the watcher without retrying an
uncertain mutation. It must implement the actual job replacement protocol.
This API alone does **not** provide graceful switchover, savepoint migration,
rollback or live updates; those remain unfinished. Cancellation stops polling
and is passed to the callback. Prefer atomic file replacement when editing;
two stable reads cannot prove a file writer has finished an in-place edit.

The coordinator supports `PUT /api/v1/jobs/{id}/checkpoint-interval` with
`{"interval":"5s"}` for a running job. This changes future periodic triggers
without redeploying tasks; `0s` disables automatic triggers. Existing checkpoint
operations retain their original settings, and manual savepoints remain enabled.
The interval is persisted with both graph and job metadata. The schedule remains
anchored to the previous trigger (or running time), so shortening an interval
can make a checkpoint immediately due. WatchLiveUpdates connects interval-only edits to this endpoint. Automatic
migration and live parallelism changes remain open.

SDK controllers can call
`pipeline.SetCoordinator(url).UpdateCheckpointInterval(ctx, jobID, interval)`.
It uses the configured coordinator security, sends one request, and verifies the
response identifies the job and requested duration. Job list/detail responses
expose `checkpoint_interval` when the job has an explicit checkpoint policy.
A failed request is not automatically retried; controllers must reconcile the
job's current status before deciding what to do after an uncertain response.

`current.PlanUpdate(candidate)` validates both named-worker deployments and
classifies them as unchanged, checkpoint-interval-only, or migration-required.
It compares complete submitted graphs and configured state backends, rather
than matching a few YAML fields. A simultaneous expression/connector/backend
edit cannot be sent through the interval-only path. Planning has no runtime
side effects. Migration-required is a decision to perform further validation,
not a guarantee that the existing savepoint restore path supports that change.

Before stopping a predecessor, a controller can call
`candidate.SetCoordinator(url).ValidateReplacement(ctx, jobID)`. This submits the
candidate graph to `POST /api/v1/jobs/{id}/replacement/validate` and checks the
current physical layout without changing the job. Operator authorization is
required. A successful response is advisory: it does not reserve the current
job, prove archive health or certify application state serializers. Restore
must still validate the actual savepoint. Changed topology/parallelism requires
a migration implementation beyond this existing-layout check.

`current.WatchLiveUpdates(ctx, path, jobID, bindings, config)` connects stable
file edits to confirmed live interval updates for an existing job. It rejects
invalid YAML without changing the job, advances a private baseline after each
successful response, and supports reverting the interval. Expression, connector,
backend or topology edits return `ErrPipelineMigrationRequired` without stopping
the job. The caller must supply the current definition and exclusively own job
configuration changes; independent external edits are not reconciled yet.
This remains interval-only reload support, not full WIP-19 migration.

For same-layout replacements, `candidate.ReplaceFromSavepoint(ctx, jobID,
savepointID)` calls `POST /api/v1/jobs/{id}/replacement`. The candidate preserves
the job name and physical layout; the coordinator requires its latest completed
savepoint and no active checkpoint. HTTP 202 means accepted for fenced teardown
and redeployment, not that the new code is running. Inspect job status and
`rescale_failure` to detect rollback. The SDK sends one request and verifies the
returned job identity; reconcile ambiguous responses before another mutation.
Rollback restores the prior graph/policies, but restarting it still requires
remaining recovery budget. This API does not itself watch files or support
changed-topology migration.

`candidate.Reload(ctx, jobID)` orchestrates same-layout preflight, creation and
completion of a savepoint, replacement acceptance, and status polling. It returns
the savepoint ID even if a later step fails. `ErrPipelineReplacementRolledBack`
means the previous configuration has been restored; its recovery may still be
in progress or may exhaust the restart budget. A successful return means the
replacement reached RUNNING or FINISHED. Bound this operation with a context.

Set `PipelineLiveWatchConfig.AllowReplacement` to enable this path for stable
non-interval file edits. `OnReload` receives its result/error, including the
retained savepoint ID. The watcher advances its baseline only on success and
stops on rollback or uncertain requests. Savepoints are retained for explicit
cleanup. Exclusive configuration ownership is still required; a periodic
checkpoint that supersedes the savepoint can cause safe rejection and must be
reconciled before another reload. Changed topology remains unsupported by this
same-layout orchestration.

### CLI watch

After submitting the initial definition, attach with:

```sh
wire jobs watch JOB_ID --file pipeline.yaml --coordinator https://host:4001 \
  --ca-cert ca.pem --api-key-file api-key.txt --allow-replacement
```

Without `--allow-replacement`, only interval edits are applied and other edits
stop the watcher. `--poll-interval` defaults to 250ms. The supplied file must
initially describe the running job, and this watcher must exclusively own its
configuration updates. It does not submit an initial job or reconcile other
controllers. Stdout contains JSON applied/reload events; reload events include
the savepoint ID for later inspection/cleanup. Ctrl-C stops watching; it does
not cancel the remote job or undo an already accepted replacement. Existing
HTTPS/client-certificate/API-key/password-file settings are supported. The
stock command binds public HTTP connectors; application-specific registries
can use the SDK watch API.

For a reproducible built-binary command smoke test, run
`python3 scripts/pipeline-watch-smoke.py /absolute/path/to/wire`. It checks watch
routing, invalid/valid edits and clean SIGINT shutdown against a fake coordinator;
real-worker replacement coverage is in the SDK runtime tests.

Watched interval requests include `expected_interval`, taken from the last
confirmed definition. The coordinator compares it atomically before persisting;
a concurrent interval change returns HTTP 409 and stops the watcher without
overwriting it. Direct UpdateCheckpointInterval remains an unconditional
explicit update. This precondition does not detect unrelated graph edits or
an interval changed away and back, so it does not replace exclusive controller
ownership for migration.
