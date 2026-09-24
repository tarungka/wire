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
an error. Named DLQ destinations currently require a legacy shared sink factory.

Checkpoint and restart settings use the SDK's local coordinator/worker runtime
and require instance-aware factories for every source and sink, including at
parallelism one. This permits a fresh connector on each deployment attempt;
sources must still implement the SDK checkpoint/restore contract for replay,
and exactly-once external output requires transactional sinks. Factory support
alone does not provide either guarantee. Checkpointed sources currently park at
EOF until all job sources exhaust. Mixed bounded/unbounded jobs therefore keep
the exhausted source task and its output streams open; independently finishing
those branches is an open lifecycle requirement. External cluster deployment and YAML
recovery acceptance remain in the completion audit.

There is no automatic reload, drain/switchover, savepoint migration, CLI loader,
or cluster deployment of CEL programs yet. Invalid reload candidates can be
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
