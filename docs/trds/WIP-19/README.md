# WIP-19: YAML Pipeline Parser

**Status:** `Partially Implemented`
**Author:** TBD
**Dependencies:** WIP-14 (User API & Go SDK)

## Implementation Status — 2026-09-12

Assessed against `master` at `0e78195`. This section records current implementation; the proposal below retains its original design context and targets.

- **Implemented:** Strict single-document parsing for the proposed schema, input/name/type validation, cycle detection, forward references, CEL compilation with bounded evaluation, all listed transform graph mappings, and conversion to the SDK StreamGraph. Stateless single-source/single-sink pipelines execute through the SDK.
- **Remaining:** Hot reload and safe switchover, savepoint migration, configuration-only live updates, CLI/worker expression deployment, and integration with parallel/keyed/window/checkpoint/restart execution. These execution capabilities are explicitly rejected rather than silently skipped. Connector types are supplied by the caller; the historical Kafka/stdout example does not make those connectors available. Existing YAML system configuration is a different feature.
- **Evidence:** [parser](../../../sdk/pipeline_yaml.go), [transform compiler](../../../sdk/pipeline_transforms.go), [CEL evaluation](../../../sdk/pipeline_expression.go), [tests](../../../sdk/pipeline_yaml_test.go), [usage and execution limits](../../../sdk/pipeline_yaml.md).

## Summary

This TRD defines a YAML-based pipeline definition language for Wire. Users will be able to define streaming pipelines declaratively in YAML files, which are parsed and converted into the same `StreamGraph` representation used by the Go SDK (WIP-14).

## Motivation

While the Go SDK provides a programmatic API for building pipelines, many use cases benefit from a declarative approach:
- Configuration-driven deployments (no recompilation)
- Separation of pipeline logic from infrastructure code
- Easier pipeline review and version control
- Non-Go developers can define pipelines

## Scope

### In Scope
- YAML pipeline schema definition
- Built-in transform types
- Expression language for simple transformations
- Pipeline validation
- Conversion to StreamGraph (shared internal representation with Go SDK)
- Hot-reload considerations

### Out of Scope
- Custom operator registration (separate TRD)
- GUI pipeline builder
- Pipeline migration tooling

## YAML Pipeline Schema

```yaml
apiVersion: wire/v1
kind: Pipeline
metadata:
  name: my-pipeline
  labels:
    team: data-eng

spec:
  parallelism: 4

  checkpoint:
    interval: 30s
    timeout: 5m

  restart:
    strategy: fixed-delay
    max-attempts: 3
    delay: 10s

  sources:
    - name: kafka-input
      type: kafka
      config:
        brokers: ["localhost:9092"]
        topic: events
        group: my-group

  transforms:
    - name: parse-json
      type: json-parse
      input: kafka-input
      config:
        target-field: payload

    - name: filter-valid
      type: filter
      input: parse-json
      config:
        expression: "payload.status != 'invalid'"

    - name: key-by-user
      type: key-by
      input: filter-valid
      config:
        key-expression: "payload.user_id"

    - name: count-window
      type: tumbling-window
      input: key-by-user
      config:
        size: 1m
        aggregation: count

    - name: select-fields
      type: select
      input: count-window
      config:
        fields: ["key", "count", "window_start", "window_end"]

  sinks:
    - name: output
      type: stdout
      input: select-fields
```

## Built-in Transform Types

| Type | Description | Config Fields |
|------|-------------|---------------|
| `json-parse` | Parse JSON from event value | `target-field` |
| `filter` | Keep events matching expression | `expression` |
| `key-by` | Partition by key expression | `key-expression` |
| `select` | Project specific fields | `fields` |
| `rename` | Rename fields | `mappings` |
| `tumbling-window` | Non-overlapping time windows | `size`, `aggregation` |
| `sliding-window` | Overlapping time windows | `size`, `slide`, `aggregation` |
| `session-window` | Activity-based windows | `gap`, `aggregation` |
| `map` | Apply expression to each event | `expression` |
| `flat-map` | Expand events via expression | `expression` |

## Expression Language

Options to evaluate:
1. **CEL (Common Expression Language)** — Google's expression language, well-suited for filtering and field access
2. **Simple field-path syntax** — Custom minimal syntax for field access (`payload.user_id`)
3. **Go templates** — Familiar but limited type safety

Recommendation: CEL for complex expressions, simple field-path for key selectors and projections.

## Pipeline Validation

The parser must validate:
- All `input` references point to defined sources or transforms
- No cycles in the transform graph
- Required config fields present per transform type
- Expression syntax validity
- Source and sink type availability

## Conversion to StreamGraph

The YAML parser produces the same `sdk.StreamGraph` used by the Go SDK:
- Each source → `StreamNode{Type: NodeSource}`
- Each transform → appropriate `StreamNode` type
- Each sink → `StreamNode{Type: NodeSink}`
- Edges derived from `input` references
- Shuffle types inferred from transform type (key-by → Hash, others → Forward)

## Hot-Reload Considerations

- File watcher detects YAML changes
- Validate new pipeline before applying
- Graceful switchover: drain current pipeline, start new one
- Savepoint-based migration when pipeline topology changes
- Config-only changes (parallelism, checkpoint interval) applied without restart

## References

- WIP-14: User API & Go SDK — defines `StreamGraph`, `StreamNode`, operator types
- Wire SDK package: `sdk/stream_graph.go`
