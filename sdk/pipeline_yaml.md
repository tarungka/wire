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
  aggregator. Positive durations are required. They are graph definitions only
  until window execution is integrated.

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

`Graph()` exposes the compiled SDK graph for integration. `Execute` currently
supports stateless linear pipelines with exactly one source, one sink, and
parallelism one. It rejects branching, key-by/window execution, parallelism above
one, periodic checkpoints, and restart policies because the underlying runtime
cannot yet honor all of those contracts. Parsing their graph/configuration does
not imply that execution is available. The SDK source lifecycle correction here
is also present in the separate HTTP connector PR.

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
