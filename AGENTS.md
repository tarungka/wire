# AGENTS.md

A navigation guide for AI agents and new contributors working in this repository.

> **Status note:** An earlier version of this file described a pre-rewrite, pipeline-based architecture with many built-in connectors. That architecture no longer exists. The codebase was fully rewritten (merged March 2026, PR [#148](https://github.com/tarungka/wire/pull/148)). This file reflects the current layout.

## What Wire is

Wire is a distributed stream processing engine: a Master–Worker system that runs streaming jobs with exactly-once semantics, embedded state (PebbleDB), and Asynchronous Barrier Snapshot checkpointing. Read [`docs/vision.md`](docs/vision.md) first for goals and non-goals, then [`docs/architecture.md`](docs/architecture.md) for the runtime topology.

## Repository layout

```
cmd/                     Single binary entry point (runs as coordinator or worker)
  main.go                Mode selection (runCoordinator / runWorker)
  init.go                CLI flag parsing
  signals.go             Signal handling

internal/
  cmd/                   Build metadata (version/commit/branch)
  config/                Config loading, validation, flag merging
  coordinator/           Control plane: job manager, scheduler, checkpoint
                         coordinator, HTTP API, leader election, Pebble
                         metadata store, recovery
  engine/                Stream processing engine: operators, barriers,
                         checkpoint coordination, state backends, DLQ,
                         watermarks, windowing
  keygroup/              Key-group assignment (state sharding primitive)
  logger/                zerolog wrappers
  protocol/              Wire protocol framing and message types (msgpack)
  rpc/                   Coordinator ↔ Worker RPC implementations
  transport/             TCP transport (Yamux multiplexing)
  worker/                Data plane: task slot management, registration,
                         heartbeats, RPC client

sdk/                     User-facing Go SDK: DataStream API, Source/Sink
                         interfaces, environments, test harness

docs/                    Canon design docs + WIP/TRD proposals
```

## Core interfaces

- **`sdk.Source`** (`sdk/source.go`): `Open`, `ReadBatch`, `Close`, `GenerateWatermark`.
- **`sdk.Sink`** (`sdk/sink.go`): `Open`, `Write`, `Close`.
- No reference connector implementations ship yet. Design for reference connectors and a connector SDK is under [WIP-16](docs/trds/WIP-16/README.md).

## Configuration

Wire loads a YAML/JSON config file (default: `.config/config.json`) and applies CLI flag overrides. See [`internal/config/`](internal/config/) for the types. A formal schema is being defined in [WIP-13](docs/trds/WIP-13/README.md).

## Build, run, test

```bash
make build      # build the wire binary
make test       # full test suite
make unittest   # unit tests only
make lint       # golangci-lint
make format     # gofmt
```

Run modes (see [`docs/usage.md`](docs/usage.md) for the full flag reference):

```bash
./wire --mode coordinator --http-listen :4001 --listen :4002 \
       --election-backend noop --coordinator-data-dir data/coordinator

./wire --mode worker --coordinator-addr localhost:4002 --task-slots 4
```

## Making changes

- **Bug fixes and small refactors:** open a PR directly.
- **New subsystems, public interfaces, connectors, or changes to the execution model:** write a WIP under [`docs/trds/`](docs/trds/README.md) first. The WIP lifecycle is `Draft → In Review → Approved → Implemented`.
- **Stale or conflicting documentation:** see [`docs/docs-todo.md`](docs/docs-todo.md) for the tracked gap list.

## Canon doc map

| Topic | Doc |
|-------|-----|
| Vision, principles | [`docs/vision.md`](docs/vision.md) |
| Runtime topology, RPC surface | [`docs/architecture.md`](docs/architecture.md) |
| Event time, checkpointing, backpressure | [`docs/execution-model.md`](docs/execution-model.md) |
| State, Pebble, snapshots | [`docs/state-backend.md`](docs/state-backend.md) |
| Deployment and operations | [`docs/operations.md`](docs/operations.md) |
| CLI flags and getting started | [`docs/usage.md`](docs/usage.md) |
| Glossary (Barrier, Key Group, Epoch, …) | [`docs/glossary.md`](docs/glossary.md) |
| Active design proposals | [`docs/trds/`](docs/trds/) |

## What is intentionally *not* in Wire (yet)

These are mentioned in some older notes but are not in the codebase today:

- Built-in connectors (Kafka, MongoDB, Elasticsearch, Redis, S3, etc.) — deleted in the rewrite; reintroduction is scoped under [WIP-16](docs/trds/WIP-16/README.md).
- Raft consensus — replaced with PebbleDB + pluggable leader election ([WIP-09](docs/trds/WIP-09/README.md)). Raft is kept as a deferred option (Phase D).
- YAML pipeline DSL — proposed in [WIP-19](docs/trds/WIP-19/README.md); not yet implemented.
- SQL interface, Web UI, Helm charts, Kubernetes operator — not on the near-term roadmap.

When in doubt, trust the code under `internal/` over any older documentation.
