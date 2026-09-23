# Documentation maintenance and remaining gaps

**Status:** Reference

**Reviewed:** 2026-09-23 against the code on `master`

The earlier backlog described the pre-SDK codebase. SDK interfaces, configuration,
protocol, connector, CLI, and recovery documentation now exist. Historical WIP
problem statements describe the system when proposed; use runtime contracts and
implementation evidence for current behavior.

## Current references

| Topic | Reference |
| --- | --- |
| Build, node startup, REST, embedded SDK example | [Usage](usage.md) |
| Configuration fields, defaults, limitations | [Reference](configuration-reference.md), [validation](configuration-validation.md) |
| Job CLI and graph submission | [Job CLI](job-cli.md) |
| HTTP source/sink and replay/delivery limits | [HTTP connector](../sdk/connectors/httpapi/README.md) |
| YAML parser and execution limits | [YAML pipelines](../sdk/pipeline_yaml.md) |
| Wire framing and serialization | [WIP-01](trds/WIP-01/README.md) |
| Goroutines, key groups, watermarks, alignment, metadata | [WIPs 02–06](trds/README.md) |
| RPC, heartbeat, HA | [RPC](trds/WIP-07/runtime-contract.md), [heartbeat](trds/WIP-08/runtime-contract.md), [HA](trds/WIP-09/runtime-contract.md) |
| Sink transactions and recovery | [WIP-10 contract](trds/WIP-10/runtime-contract.md) |
| Error policies and DLQ | [Usage](sdk/error_handling.md) |
| Metrics and rescale | [Observability](observability.md), [rescale safety](rescale-safety.md) |
| Terminology | [Glossary](glossary.md) |

## Remaining work

- Add a complete distributed application tutorial that registers named worker
  factories, submits a graph, and demonstrates checkpoint/replay. The embedded
  quick start and graph-envelope reference do not replace this tutorial.
- Consolidate a generated REST reference, including response fields and errors
  for checkpoints and rescale, with compatibility/versioning rules.
- Keep historical WIP implementation snapshots clearly dated; refresh them when
  their scope changes. An old PR-status paragraph is not current merge evidence.
- Replace the [technical outline](techinical-documentation.md) with maintained
  content or archive it; its unanswered design questions are not runtime contracts.
- Extend automated documentation checks beyond the generated configuration table:
  compile SDK examples, validate local links, and compare CLI/metric inventories.

## Limits documentation must preserve

Pause/resume do not implement a completed task suspension/restore workflow.
YAML execution is restricted to stateless linear pipelines with one source,
one sink, and parallelism one, without periodic checkpoints or restart policies.
Embedded execution rejects transactional sinks. Connector replay and transaction
requirements are explicit contracts, not properties inferred from implementing
basic Source/Sink methods. No managed broadcast-state API ships today.

## Maintenance procedure

For a behavior change, update its user-facing guide and runtime contract in the
same PR. Regenerate the configuration reference with the command at its top when
config types/defaults change. Keep illustrative placeholders labeled, and validate
runnable examples against the module's Go version. Archived documents under
`old/` and proposal problem statements are historical material, not current setup
instructions.
