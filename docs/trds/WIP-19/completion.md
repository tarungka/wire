# WIP-19 completion audit

This follow-up preserves the entire proposal, including hot reload and live
configuration changes. It is not complete. It is stacked on WIP-18 to use the
SDK, connector, security and state integrations from WIP-13 through WIP-18.

| Requirement | Current evidence and remaining work |
| --- | --- |
| Strict YAML schema | Existing single-document, known-field parser and graph validation; schema field-coverage tests. Full original field and invalid-input audit remains. |
| Transform types and expressions | All listed transforms compile through existing CEL and SDK graph code. Distributed serialization, worker registration and execution remain required. |
| Connector availability | Caller-provided source/sink factories are validated before construction. WIP-16 connector registry and worker factory integration remain required. |
| Graph conversion | Existing SDK graph construction, forward references and cycle rejection. Validate shuffle semantics against current SDK and parallel execution. |
| Pipeline state backend | `spec.state_backend` accepts WIP-18 nested HashMap/Pebble configuration. Validation runs before connector construction; omitted HashMap limit is 256 MiB and explicit zero is unlimited. SDK override has precedence. Full CLI/pipeline/system precedence remains open. |
| Parallel/keyed/window execution | Current YAML executor still rejects parallel connector execution and checkpoint/restart settings. Remove restrictions only when per-instance factories, worker execution and recovery are proven. Existing single-instance window execution remains available. |
| Checkpoint and restart | Integrate configured policies with actual coordinator/worker execution and test failure recovery. |
| File watching and validation | Detect edits and validate a complete replacement before touching the current run. Not implemented. |
| Graceful switchover | Drain old execution and start the validated replacement without overlapping ownership. Not implemented. |
| Topology changes | Savepoint-based migration and failure rollback. Not implemented. |
| Configuration-only changes | Apply parallelism and checkpoint interval changes without job restart, as specified. Existing APIs alone do not prove this behavior. Not implemented. |
| CLI and operations | Add executable pipeline submission/watch paths, examples and upgrade/security guidance. Audit all commands using the built binary. |

The historical Kafka/stdout example remains illustrative: it does not imply
that those connectors are bundled. No requirement above is removed because
another WIP already has a helper or because a parser test passes.
