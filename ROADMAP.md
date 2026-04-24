# Wire Roadmap

> **Status note:** An earlier version of this file listed dated quarterly milestones (Q1–Q4 2025) and features from a pre-rewrite architecture (Raft, BadgerDB, MongoDB/Kafka/Elasticsearch connectors). Those milestones never landed and the architecture that would have carried them no longer exists. The codebase was fully rewritten (merged March 2026, PR [#148](https://github.com/tarungka/wire/pull/148)). This file now tracks real, in-flight work.
>
> Individual proposals (and all design decisions) live under [`docs/trds/`](docs/trds/). This file is only a shortcut view.

## Current state

- Pre-`v0.1.0`, alpha.
- Coordinator (control plane), Worker (data plane), engine, state backend, and checkpoint coordinator are implemented.
- User-facing surfaces (config schema, Go SDK, REST API, connector SDK, security model) are being specified via Wire Improvement Proposals (WIPs) before implementation.
- There are no built-in connectors yet.

## In-flight WIPs

The full index lives in [`docs/trds/README.md`](docs/trds/README.md). Status reflects the state of each WIP at the time of writing; read the WIP itself for the latest.

| WIP | Topic | Status |
|-----|-------|--------|
| [WIP-01](docs/trds/WIP-01/README.md) | Wire Protocol & Serialization Format | Draft |
| [WIP-02](docs/trds/WIP-02/README.md) | Goroutine & Concurrency Model | Draft |
| [WIP-03](docs/trds/WIP-03/README.md) | Key Group Assignment & State Sharding | Draft |
| [WIP-04](docs/trds/WIP-04/README.md) | Watermark Generation Algorithm | Draft |
| [WIP-05](docs/trds/WIP-05/README.md) | Barrier Alignment Timeout & Failure Handling | Draft |
| [WIP-06](docs/trds/WIP-06/README.md) | Checkpoint Metadata Schema | Draft |
| [WIP-07](docs/trds/WIP-07/README.md) | RPC Interface Specification | Draft |
| [WIP-08](docs/trds/WIP-08/README.md) | Heartbeat & Health Monitoring | Draft |
| [WIP-09](docs/trds/WIP-09/README.md) | Coordinator High Availability | Draft |
| [WIP-10](docs/trds/WIP-10/README.md) | Two-Phase Commit for Transactional Sinks | Draft |
| [WIP-11](docs/trds/WIP-11/README.md) | Error Handling & Dead Letter Queues | Draft |
| [WIP-12](docs/trds/WIP-12/README.md) | Late Data & Allowed Lateness | Draft |
| [WIP-13](docs/trds/WIP-13/README.md) | Configuration Reference | Draft |
| [WIP-14](docs/trds/WIP-14/README.md) | User API & Go SDK | Draft |
| [WIP-15](docs/trds/WIP-15/README.md) | Job Lifecycle & REST API | Draft |
| [WIP-16](docs/trds/WIP-16/README.md) | Connector SDK & Built-in Connectors | Draft |
| [WIP-17](docs/trds/WIP-17/README.md) | Security Model | Draft |
| [WIP-18](docs/trds/WIP-18/README.md) | Multiple State Backends | Draft |
| [WIP-19](docs/trds/WIP-19/README.md) | YAML Pipeline Parser | Proposed |
| [WIP-20](docs/trds/WIP-20/README.md) | Task Execution Engine | Draft |

## Near-term focus (in priority order)

These are the items most worth tackling before a `v0.1.0` release. They are intentionally short-term and pragmatic — each unblocks either contributors or end users.

1. **Finish the four coordinator TODOs** that block end-to-end fault tolerance and rescaling:
   - Pebble state backend wiring (`internal/engine/state_backend_factory.go`, [WIP-18](docs/trds/WIP-18/README.md))
   - Savepoint restore (`internal/coordinator/job_manager.go`)
   - Barrier injection via RPC (`internal/coordinator/savepoint_manager.go`)
   - Task rescheduling on worker removal (`internal/coordinator/http_cluster.go`)

2. **Promote WIP-13, WIP-14, WIP-15, WIP-16 from Draft to Approved.** These are the user-facing surfaces (config, SDK, REST API, connector SDK) that unblock anyone actually running Wire.

3. **Ship a reference source/sink pair** (e.g. stdin source, file sink) as the first in-tree connector. Smallest change that makes Wire runnable end-to-end, and validates the `Source`/`Sink` interfaces in [`sdk/`](sdk/) against real code.

4. **Close P1 gaps in [`docs/docs-todo.md`](docs/docs-todo.md):** RPC spec, wire protocol spec, 2PC for transactional sinks, security model. All have corresponding WIPs.

## How to propose new work

See [`docs/trds/README.md`](docs/trds/README.md) for the WIP process. Small changes do not need a WIP; anything that touches public surfaces, the execution model, or multiple modules does.
