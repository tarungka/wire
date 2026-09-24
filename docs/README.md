# Wire Documentation

Central index for all Wire project documentation.

---

## Active Documents

| File | Description | Status |
|------|-------------|--------|
| [vision.md](vision.md) | System contract — what Wire is, its guarantees, and design principles | Canon v1.0.0 |
| [architecture.md](architecture.md) | Runtime structure — Coordinator/Worker topology and components | Canon v1.0.0 |
| [execution-model.md](execution-model.md) | Engine physics — events, time semantics, watermarks, and windowing | Canon v1.0.0 |
| [state-backend.md](state-backend.md) | Persistence — state model, Pebble backend, and checkpointing | Canon v1.0.0 |
| [operations.md](operations.md) | Production runtime — deployment, scaling, monitoring, and failure recovery | Canon v1.0.0 |
| [diagrams.md](diagrams.md) | Visual reference — Mermaid architecture, data flow, checkpointing, and state diagrams | Canon v1.0.0 |
| [techinical-documentation.md](techinical-documentation.md) | Full engineering spec — strategy, API surface, GPU design, and connectors | Draft v0.1.0 |
| [gemini-conversations-export.md](gemini-conversations-export.md) | Exported Gemini research conversations — actor model, PebbleDB, Flink internals, language choice | Reference |
| [usage.md](usage.md) | Getting started — building, running, HTTP API reference, and SDK quick start | Canon v1.0.0 |
| [glossary.md](glossary.md) | Glossary of Wire-specific terms and definitions | Reference |

## User-facing references

- [Configuration reference](configuration-reference.md) and [validation](configuration-validation.md)
- [Job-management CLI](job-cli.md) and [rescale safety](rescale-safety.md)
- [HTTP API connector](../sdk/connectors/httpapi/README.md)
- [YAML pipelines and execution limits](../sdk/pipeline_yaml.md)
- [Error policies and DLQ](sdk/error_handling.md)
- [Observability](observability.md)
- [Documentation maintenance and remaining gaps](docs-todo.md)

The Canon pages describe the current runtime at a high level. Feature-specific
runtime contracts document detailed guarantees and limits. WIP proposal text,
dated implementation snapshots, drafts, and archived documents may describe an
earlier codebase or future scope.

## Conventions

- **Status labels:** `Canon` = authoritative and stable, `Draft` = work-in-progress
- **Versioning:** semver (`MAJOR.MINOR.PATCH`)
- **Naming:** lowercase kebab-case (e.g., `state-backend.md`)
- **Archived docs:** Pre-rewrite documentation lives in [`old/`](old/) for reference

## WIRE Improvement Program (WIPs)

Significant changes and additions to Wire are proposed through **WIPs** — structured design documents that live in [`trds/`](trds/).

Each WIP gets its own folder (`WIP-01/`, `WIP-02/`, ...) containing a `README.md` with the proposal. See the [WIP process guide](trds/README.md) for details.

## Flink Comparison

The [`flink/`](flink/) folder contains a detailed side-by-side comparison of Wire's design against Apache Flink's architecture. See [wire-vs-flink-analysis.md](flink/wire-vs-flink-analysis.md).
