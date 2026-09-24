# WIP-13: Configuration reference

**Status:** Implemented (configuration reference scope)
**Updated:** 2026-09-24

## Contract

WIP-13 defines and documents node/pipeline configuration formats, defaults,
precedence, environment substitution, CLI overrides and validation. It does not
implement the runtime subsystems configured by those documents. Declarative
pipeline execution belongs to WIP-19 and HTTP security to WIP-17; their remaining
limitations are explicitly documented rather than implied to work.

The pre-rewrite proposal's Raft, discovery and join flags are obsolete and have
been removed from this reference. There are no `--raft-addr`, `--http-addr`,
`--auth`, or `--node-verify-server-name` flags. Use the actual flag inventory.

- [Node fields, defaults and CLI mappings](../../configuration-reference.md).
- [All node CLI flags and defaults](../../usage.md); [job CLI](../../job-cli.md).
- [Node JSON Schema](../../schemas/wire.schema.json).
- [Pipeline JSON Schema](../../schemas/pipeline.schema.json) and
  [parser semantics and execution boundaries](../../../sdk/pipeline_yaml.md).
- [Precedence, environment syntax, validation rules](../../configuration-validation.md).
- [Cluster walkthrough](../../configuration-walkthrough.md), with tested
  [coordinator](../../examples/coordinator.yaml),
  [worker](../../examples/worker.yaml) and [pipeline](../../examples/pipeline.yaml)
  examples.

Precedence is defaults → files in argument order → `WIRE_*` overrides → string
substitution → explicitly supplied flags. Files support YAML/YML and JSON.
Unknown node keys remain ignored for compatibility; schemas reject them during
authoring. No dynamic node configuration reload or migration tooling is added.
All semantic errors are collected after merging; syntax/type errors fail loading.

## Acceptance

| Requirement | Evidence |
| --- | --- |
| Complete node fields/defaults/flag mapping | `TestConfigurationReference`, generated table |
| Machine-readable node format | `TestConfigurationSchema`, generated JSON Schema |
| Pipeline format and example | `TestPipelineSchemaFieldCoverage`, `TestDocumentedPipelineExample` |
| File overlays and explicit flags | loader/flags test suites |
| Environment overrides, including types and precedence | `TestEnvironmentOverridePrecedence`, `TestInvalidEnvironmentOverrides` |
| Substitution including HA lists and replica paths | `TestEnvironmentSubstitutionHAAndReplicaFields`, existing substitution tests |
| Actionable missing-variable location | `TestEnvironmentSubstitutionIdentifiesListElement` |
| Documented node examples validate | `TestDocumentedNodeExamples` |
| Validation rules and boundaries | config validation, heartbeat, HA, replica, task-slot and checkpoint-policy tests |

Schema checks are intentionally structural: filesystem existence, cross-field
comparisons, connector-specific settings and execution support remain runtime
validation responsibilities. Node schema generation and pipeline field coverage
prevent new config fields silently disappearing from the published formats.

## Compatibility

`WIRE_*` overrides are now active; deployments already exporting these exact
names will apply them. Review the environment before upgrading. Values are typed;
string arrays use JSON (for example `WIRE_WORKER_COORDINATOR_SEEDS='["host:4002"]'`).
Unknown environment names are ignored. Existing `${VAR:-default}` behavior keeps
an explicitly empty variable empty. New fields automatically receive substitution.

HTTP authentication/TLS settings do not become operational merely because they
appear in a schema. No new connector is implied by a pipeline `type` string.

Verification on this branch: config and SDK race suites, build and golangci-lint
v2.5.0 pass. A live binary loaded the documented coordinator/worker files with
temporary port/directory overrides, reached readiness, and reported an ALIVE
worker with four available slots. Both processes were stopped after the check.
