# Wire

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.24-blue.svg)](go.mod)

Wire is a distributed stream processing engine written in Go. It targets unbounded data streams with strict correctness guarantees: exactly-once semantics, deterministic recovery, and strict event ordering. State lives in an embedded PebbleDB; checkpoints use Asynchronous Barrier Snapshots (Chandy–Lamport). Wire is designed to run as a single binary with no external dependencies.

> **Project status:** pre-`v0.1.0`, alpha. The codebase underwent a full architectural rewrite (merged March 2026, PR [#148](https://github.com/tarungka/wire/pull/148)). The engine, coordinator, and worker are in place; user-facing surfaces (configuration reference, Go SDK, REST API, connector SDK) are actively being specified under the [WIP process](docs/trds/). There are no built-in connectors yet.

## Documentation

The canonical design docs live in [`docs/`](docs/):

| Doc | Purpose |
|-----|---------|
| [`docs/vision.md`](docs/vision.md) | Goals, non-goals, and design principles |
| [`docs/architecture.md`](docs/architecture.md) | Runtime topology, control plane, data plane |
| [`docs/execution-model.md`](docs/execution-model.md) | Event/time/watermarks, checkpointing, backpressure |
| [`docs/state-backend.md`](docs/state-backend.md) | Pebble integration, snapshot protocol |
| [`docs/operations.md`](docs/operations.md) | Deployment modes, scaling, monitoring |
| [`docs/usage.md`](docs/usage.md) | Build/run instructions and CLI flag reference |
| [`docs/trds/`](docs/trds/) | Wire Improvement Proposals (WIPs) — design history and in-flight proposals |

For a gap analysis against the current codebase, see [`docs/docs-todo.md`](docs/docs-todo.md). For the project roadmap, see [`ROADMAP.md`](ROADMAP.md).

## Build

```bash
git clone https://github.com/tarungka/wire.git
cd wire
make build
```

This produces the `wire` binary in the project root. Go 1.24+ is required.

## Run

Wire runs as a single binary in either coordinator or worker mode. See [`docs/usage.md`](docs/usage.md) for the full flag reference.

**Coordinator (single-node):**

```bash
./wire \
  --mode coordinator \
  --http-listen :4001 \
  --listen :4002 \
  --election-backend noop \
  --coordinator-data-dir data/coordinator
```

**Worker:**

```bash
./wire \
  --mode worker \
  --coordinator-addr localhost:4002 \
  --task-slots 4
```

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for development setup and contribution guidelines. Non-trivial changes should go through the [WIP process](docs/trds/README.md).

## License

MIT — see [`LICENSE`](LICENSE).
