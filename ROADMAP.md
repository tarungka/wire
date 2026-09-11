# Wire Roadmap

> **Status note:** An earlier version of this file listed dated quarterly milestones (Q1–Q4 2025) and features from a pre-rewrite architecture (Raft, BadgerDB, MongoDB/Kafka/Elasticsearch connectors). Those milestones never landed and the architecture that would have carried them no longer exists. The codebase was fully rewritten (merged March 2026, PR [#148](https://github.com/tarungka/wire/pull/148)). This file now tracks real, in-flight work.
>
> Individual proposals (and all design decisions) live under [`docs/trds/`](docs/trds/). This file is only a shortcut view.

## Current state

- Pre-`v0.1.0`, alpha.
- Coordinator metadata persistence, engine primitives, and named forward-only worker pipelines are implemented. Distributed stateful execution, checkpoint-based recovery, and exactly-once cluster output remain incomplete.
- Configuration loading, the Go SDK, and REST endpoints exist, with important gaps described in their WIPs. Memory connectors support tests; the proposed HTTP reference connector is not implemented.
- WIP statuses were audited against `master` at `0e78195` on 2026-09-12. Partially Implemented means some scoped work exists, not that its cluster guarantee is available.

## WIP status

The maintained status table is the [complete WIP index](docs/trds/README.md#wip-index). Each proposal has a dated implementation section identifying completed work, remaining work, and source evidence.

- **Implemented:** WIP-21, WIP-22, WIP-24, WIP-25.
- **Partially Implemented:** WIP-01 through WIP-20, WIP-23.

## Near-term focus (in priority order)

1. **Make the supported execution contract explicit.** Validate linear cluster graph shapes, reconcile task lifecycle semantics (WIP-20), and reject unsupported pause/restore operations until implemented (WIP-15).
2. **Complete the distributed stateful execution path.** Integrate keyed routing/state transfer, Pebble engine state, barrier trigger/ACK/abort handling, source replay, sink commits, and checkpoint-based recovery (WIP-03, WIP-05 through WIP-10, WIP-18). Validate with fault tests; this is more than closing isolated TODOs.
3. **Finish user-facing functionality.** Complete window/late-data execution (WIP-12/WIP-14), real savepoints and restore (WIP-15), the HTTP reference source/sink (WIP-16), and runtime security wiring (WIP-17).
4. **Reconcile specifications and measure remaining optimizations.** Update historical protocol/config/API assumptions, and evaluate the remaining selective-NoSync proposal and latency targets in WIP-23. WIP-19 now parses/validates YAML pipelines and executes stateless linear graphs; runtime integration and hot reload remain.

## How to propose new work

See [`docs/trds/README.md`](docs/trds/README.md) for the WIP process. Small changes do not need a WIP; anything that touches public surfaces, the execution model, or multiple modules does.
