# Current Capabilities

**Status:** Reference
**Version:** 0.1.0
**Context:** Truthful implementation boundary for the current codebase

---

## Purpose

The canon docs describe Wire's intended architecture. This document describes
what the current implementation can actually do today, and which guarantees are
still target capabilities.

Read this before relying on the higher-level architecture, execution model, or
Flink comparison docs.

## Summary

Wire currently has a useful coordinator/worker skeleton, SDK, embedded executor,
operator registry, transport layer, and engine primitives. It does not yet have a
complete distributed stateful execution path.

The supported cluster runtime is currently a forward-only named-operator chain:

```text
SourceNamed -> MapNamed/FlatMapNamed/FilterNamed -> SinkNamed
```

The following are target capabilities, not production-ready cluster guarantees:

- distributed keyed shuffle,
- durable Pebble-backed engine state,
- cluster exactly-once execution,
- real savepoints and restore,
- job recovery from checkpoint,
- distributed keyed windows,
- production connectors,
- HTTP API authentication and fully wired TLS.

## Capability Matrix

| Area | Current status | Notes |
|------|----------------|-------|
| Coordinator process | Implemented | Runs as a single leader with metadata persistence. |
| Worker process | Implemented | Registers with coordinator and receives task commands. |
| Worker registry | Implemented | Supports named source, map, flatmap, filter, and sink factories. |
| HTTP job lifecycle API | Partially implemented | Submit/list/get/cancel/pause/resume endpoints exist. Binary job submission is not implemented. |
| Coordinator metadata store | Implemented | PebbleDB store is used for coordinator metadata. |
| Embedded SDK execution | Partially implemented | Useful for local execution and tests. It is not the same guarantee surface as cluster mode. |
| Cluster SDK execution | Limited | Requires named operators. Closure-based SDK functions are embedded-only. |
| Cluster graph shape | Limited | Only forward edges are supported by the coordinator scheduler. |
| Distributed keyed shuffle | Not implemented | `KeyBy` creates hash shuffle, but cluster scheduling rejects shuffle edges. |
| Worker physical task model | Limited | Current worker execution is a single linear chain; source/intermediate/sink network task roles are not implemented. |
| Engine HashMap state backend | Implemented | Useful for tests and small embedded/local jobs. |
| Engine Pebble state backend | Not implemented | The factory currently returns an error for Pebble-backed engine state. |
| Watermark and barrier primitives | Partially implemented | Engine-level primitives and tests exist, but they are not wired through cluster execution end to end. |
| Cluster checkpointing | Not implemented end to end | Workers do not perform real snapshot commands in the cluster path. |
| Exactly-once cluster semantics | Not implemented | Requires cluster checkpointing, source offsets, state restore, and sink commit handling. |
| Savepoint API | Metadata only | Savepoint records can be created, but no real state snapshot or restore path exists. |
| Recovery | Metadata recovery only | Coordinator state can be recovered from store; running jobs are not restored from checkpoints. |
| Windows | Target capability in cluster mode | SDK/window primitives exist, but distributed keyed windows need shuffle and managed state first. |
| Production connectors | Not implemented | Only in-memory SDK connectors exist for tests. |
| TLS and auth | Partially implemented config surface | Config and transport pieces exist, but HTTP auth/TLS and node TLS are not fully wired through the main runtime. |
| Observability | Partially implemented | Metrics surfaces exist; per-operator/checkpoint/backpressure visibility needs more work. |

## Current Cluster Contract

Cluster mode currently supports:

- named source operators registered on workers,
- named map, flatmap, and filter operators registered on workers,
- named sink operators registered on workers,
- forward edges only,
- finite task assignment to registered workers,
- basic task status updates and cancellation.

Cluster mode currently rejects or cannot correctly execute:

- closure-based SDK operators such as `Map(func...)`,
- `KeyBy` and any hash shuffle edge,
- rebalance and broadcast shuffle,
- process/reduce/window operators,
- durable operator state,
- checkpoint restore,
- real savepoints,
- recovery of a running job after worker loss.

## Documentation Rule

Use these terms consistently:

- **Implemented:** code path exists and has tests for the advertised behavior.
- **Partially implemented:** important pieces exist, but the end-to-end user
  guarantee is incomplete.
- **Target capability:** design is documented, but users must not rely on it in
  current cluster mode.

Canon docs may describe target architecture. User-facing usage docs should state
the current boundary explicitly and link back to this file when a capability is
not implemented end to end.

## Promotion Criteria

Move a capability from target to implemented only when the code has an
end-to-end acceptance test for the actual cluster behavior.

Minimum promotion criteria:

- **Distributed keyed shuffle:** a multi-worker integration test proves
  `SourceNamed -> KeyBy -> SinkNamed` routes equal keys to the same downstream
  subtask.
- **Pebble engine state:** a state backend test proves checkpoint and restore
  using the Pebble implementation.
- **Cluster checkpointing:** a cluster test proves barriers trigger task
  snapshots, worker ACKs, and completed checkpoint metadata.
- **Exactly-once cluster semantics:** a fault test proves state continuity after
  restart from a completed checkpoint.
- **Savepoints:** a test proves a savepoint contains real state and can restore
  a job.
- **Windows:** a distributed keyed window test proves watermark-triggered output
  and state cleanup.
