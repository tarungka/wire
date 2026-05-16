# Wire vs. Flink Readiness Review

**Status:** Reference
**Version:** 0.1.0
**Context:** Senior architecture review of what Wire needs before it can credibly compete with Apache Flink

---

## Purpose

This document reviews Wire against the stated goal of rivaling Apache Flink.
It separates:

- what Wire has today,
- what Flink already does,
- what Wire is missing,
- and how Wire should implement the missing pieces without blindly cloning Flink.

This is an implementation planning document. Treat the canon docs as the target
architecture and this document as the current readiness gap.

## Executive Assessment

Wire is not close to rivaling Flink yet. It is a promising stream-processing
kernel plus a control-plane prototype.

The main problem is not code style or small missing features. The distributed
runtime does not yet support the core capabilities that make Flink useful for
production stateful streaming:

1. distributed keyed shuffle,
2. managed durable operator state,
3. coordinated checkpointing in the cluster path,
4. restore and replay after failure,
5. savepoints and rescaling,
6. production connectors,
7. operational hardening.

Wire should not try to match all of Flink immediately. The right first target is
a boring, correct, distributed, stateful pipeline:

```text
Source -> Map -> KeyBy -> StatefulProcess/Reduce -> Sink
```

That pipeline should run across multiple workers, checkpoint state, survive a
worker crash, and resume from the last completed checkpoint.

Until that works, adding SQL, a Web UI, more connectors, or advanced windows will
not materially move Wire toward Flink-class credibility.

## Current Wire Strengths

Wire already has several good foundations:

- A coordinator/worker runtime with job submission, worker registration, task
  assignment, status updates, and cancellation.
- A Yamux-based RPC/transport stack with framed messages and CRC checks.
- An SDK with embedded execution and cluster submission.
- A worker operator registry for named source/map/flatmap/filter/sink factories.
- Operator-chain execution with source readers, watermarks, barriers, DLQ/error
  handling, and transactional sink hooks in the engine package.
- Coordinator metadata persistence in PebbleDB.
- Key-group assignment primitives.
- Tests around protocol, scheduler, coordinator persistence, worker execution,
  watermarks, barrier alignment, state backend, and transport.

These are real assets. The problem is that many of them are not connected into a
complete distributed stateful execution path yet.

## Critical Gaps

### 1. Distributed Shuffle Is Missing

**What Flink does**

Flink partitions keyed streams so that all records for the same key are routed
to the same logical key group and downstream task. Flink documents key groups as
the atomic unit for redistributing keyed state during rescaling and recovery.

Official reference: [Flink Stateful Stream Processing](https://nightlies.apache.org/flink/flink-docs-release-1.20/docs/concepts/stateful-stream-processing/)

**What Wire does today**

Wire's SDK exposes `KeyBy`, which creates a hash shuffle edge. But the
coordinator rejects any graph with hash, rebalance, or broadcast shuffle:

- `sdk/data_stream.go`: `KeyBy` creates a `ShuffleHash` edge.
- `internal/coordinator/scheduler.go`: `hasShuffleEdge` causes scheduling to
  fail with "cross-worker shuffle is not yet supported".

This means Wire cannot currently run a distributed keyed stream. Without keyed
shuffle, there is no practical path to scalable keyed state, windows, joins, or
stateful aggregations.

**Recommendation for Wire**

Implement keyed shuffle before adding more user-facing features.

Minimum design:

- Split `JobGraph` into physical task stages at shuffle boundaries.
- Populate `TaskDescriptor.Upstream` and `TaskDescriptor.Downstream`.
- Use the existing `transport.Mux`/`FrameStream` for task-to-task data channels.
- Route records by `keygroup.Hash(key) -> key group -> downstream subtask`.
- Preserve ordering per key group.
- Propagate watermarks and checkpoint barriers through the same streams.
- Add an end-to-end integration test with at least two workers and one keyed
  stateful operator.

Do not implement all shuffle strategies at once. Start with hash shuffle only.
Forward can remain a local chain optimization. Rebalance and broadcast can come
later.

### 2. Worker Execution Is Still Phase 1

**What Flink does**

Flink compiles a logical dataflow into a physical execution graph. It chains
compatible operators into tasks, but breaks chains at network shuffle
boundaries. TaskManagers execute task subtasks and exchange records over the
network.

Official reference: [Flink concepts documentation](https://nightlies.apache.org/flink/flink-docs-stable/docs/concepts/overview/)

**What Wire does today**

`internal/worker/task_executor.go` states its current scope directly:

```text
Phase 1: single-input linear pipeline, no shuffle, no state, no checkpoints.
```

The executor builds one fused chain with a source, operators, and a sink. It
does not use `TaskSlot`'s network inputs/outputs. It discards forwarded output
messages because the sink is assumed to be inside the same chain.

**Recommendation for Wire**

Turn the worker executor into a real task runner:

- Source tasks produce records to downstream network outputs.
- Intermediate tasks read from upstream streams and write to downstream streams.
- Sink tasks consume from upstream streams and do not require a source.
- Support multiple inputs only after single-input shuffle works.
- Use `engine.TaskSlot` as the long-term execution primitive rather than
  duplicating channel wiring in `task_executor`.

The worker should stop assuming every task contains a source. A task can be a
source task, an intermediate task, or a sink task.

### 3. Engine Pebble State Backend Is Not Implemented

**What Flink does**

Flink has managed keyed and operator state. State is integrated with
checkpointing and recovery. Flink offers multiple state backends, including
heap-based state and RocksDB-based state, and the backend participates in
snapshot creation.

Official references:

- [Flink Stateful Stream Processing](https://nightlies.apache.org/flink/flink-docs-release-1.20/docs/concepts/stateful-stream-processing/)
- [Flink State Backends](https://nightlies.apache.org/flink/flink-docs-stable/docs/ops/state/state_backends/)

**What Wire does today**

Coordinator metadata uses PebbleDB, but engine operator state does not. The
engine's Pebble state backend is a placeholder that returns an error. The only
working engine backend is the in-memory HashMap backend.

This contradicts the canon `state-backend.md`, which describes Pebble as the
default embedded state backend.

**Recommendation for Wire**

Implement `PebbleStateBackend` before claiming durable state.

Minimum design:

- One Pebble instance per task or per task slot.
- Composite keys using `[key-group][operator-id][user-key][namespace]`.
- Prefix iteration for window cleanup and key-group migration.
- Snapshot via Pebble checkpoint/hard links.
- Restore by replacing the task state directory from a snapshot handle.
- State backend metrics for size, checkpoint duration, restore duration, and
  compaction pressure.

Wire should keep the HashMap backend for tests and small embedded jobs, but
Pebble must be the production default.

### 4. Exactly-Once Is Not Wired Through Cluster Execution

**What Flink does**

Flink uses coordinated checkpoints. Barriers flow with records. On failure,
Flink restores operator state from a completed checkpoint and replays source
records from the recorded positions. Flink's docs are careful that
"exactly-once" refers to state consistency, while external sinks require
idempotent or transactional behavior.

Official reference: [Flink Fault Tolerance](https://nightlies.apache.org/flink/flink-docs-stable/docs/learn-flink/fault_tolerance/)

**What Wire does today**

Wire has local barrier and checkpoint components, but the cluster path does not
yet trigger real checkpoints:

- `CommandTypeTakeSnapshot` is logged as a stub in the worker.
- Savepoint triggering persists metadata but does not inject barriers.
- Worker task execution passes `nil` for checkpoint ACK and transactional sink
  handling.
- Coordinator metadata recovery aborts in-flight checkpoint records, but there
  is no full restore and replay workflow.

So Wire cannot currently claim exactly-once cluster execution.

**Recommendation for Wire**

Implement one cluster checkpoint lifecycle end to end:

1. Coordinator triggers checkpoint `N`.
2. Source tasks inject barrier `N`.
3. Barriers flow through task-to-task streams.
4. Each task snapshots operator and backend state.
5. Workers ACK `N` with state handles and source offsets.
6. Coordinator marks checkpoint `N` complete only after every required ACK.
7. Failed/incomplete checkpoints are never used for recovery.

For v1, support one checkpoint in flight. Concurrent checkpoints and unaligned
checkpoints can wait.

### 5. Recovery Is Metadata Recovery, Not Job Recovery

**What Flink does**

Flink recovers by restarting failed tasks or jobs from the latest completed
checkpoint. It restores managed state and resumes sources from checkpointed
positions.

Official reference: [Flink Fault Tolerance](https://nightlies.apache.org/flink/flink-docs-stable/docs/learn-flink/fault_tolerance/)

**What Wire does today**

Wire can recover coordinator metadata from Pebble. That is useful, but it is not
the same as recovering a running job. There is no complete workflow that:

- detects worker loss,
- cancels surviving tasks,
- selects latest completed checkpoint,
- restores task state,
- rewinds sources,
- and redeploys the graph.

`ResumeJob` also contains a TODO for redeploying from a savepoint.

**Recommendation for Wire**

Add a job-attempt model:

- `JobAttemptID` or attempt counter.
- Every task assignment belongs to a specific attempt.
- Worker status updates include attempt/epoch and stale updates are rejected.
- On task/worker failure, transition `RUNNING -> FAILING -> RESTARTING`.
- Select latest completed checkpoint.
- Redeploy all tasks with `CheckpointRestoreInfo`.
- Transition back to `RUNNING` only after all tasks report restored/running.

Do not do partial task recovery first. Full-job restart is simpler and matches
the ABS model.

### 6. Savepoints Are Only Metadata

**What Flink does**

Flink savepoints are manually triggered, durable snapshots used for upgrades,
rescaling, migrations, and controlled restarts. Flink also has operator IDs/UIDs
so state can be mapped back to the correct operator after code changes.

Official reference: [Flink Savepoints](https://nightlies.apache.org/flink/flink-docs-release-2.0/docs/ops/state/savepoints/)

**What Wire does today**

Wire can create a `SavepointMeta` row, but it does not create a real state
snapshot. There is no stable operator UID strategy, no restore from savepoint,
and no rescaling workflow.

**Recommendation for Wire**

Make savepoints a thin wrapper around the checkpoint lifecycle:

- Trigger a checkpoint with type `Savepoint`.
- Store it in a user-owned savepoint path.
- Require stable operator IDs in cluster mode.
- Fail restore if required operator state cannot be matched.
- Later add a `--allow-non-restored-state` style option for removed operators.

For Wire, start with one savepoint format: native Pebble. A portable canonical
format can come later.

### 7. Stateful SDK Surface Is Ahead Of Cluster Runtime

**What Flink does**

Flink's DataStream API, keyed streams, windows, process functions, state, and
runtime execution model are integrated. A program that compiles can generally
run on the cluster, modulo connector/runtime configuration.

**What Wire does today**

Wire's SDK exposes `KeyBy`, windows, process functions, and state-like APIs, but
cluster mode supports only named source/map/flatmap/filter/sink operators.
Cluster validation requires every node to have a `ClassName`, which means
closure-based SDK jobs are embedded-only.

This split is reasonable for Go, but the user experience needs to be explicit:
embedded mode and cluster mode are different programming models today.

**Recommendation for Wire**

For the next few milestones, document two APIs clearly:

- Embedded SDK: local testing, closure-based functions, in-memory sources/sinks.
- Cluster SDK: named operators compiled into the worker binary.

Do not hide this. Make cluster validation errors actionable. Provide a complete
sample worker binary that registers named operators and runs a cluster job.

### 8. Connectors And Source Offsets Are Missing

**What Flink does**

Flink's production value comes heavily from connectors. Sources participate in
checkpointing by recording offsets/splits; sinks participate through idempotent
or transactional commit protocols.

Official reference: [Flink connectors documentation](https://nightlies.apache.org/flink/flink-docs-stable/docs/connectors/datastream/overview/)

**What Wire does today**

Wire has Source/Sink interfaces and an in-memory connector for tests. There are
no production connectors and no checkpointed source-offset contract in the
cluster path.

**Recommendation for Wire**

Do not build ten connectors. Build two reference connectors well:

1. File source/sink for deterministic integration tests.
2. Kafka source/sink or NATS source/sink for real streaming workloads.

Each source must expose checkpointable position state. Each sink must document
whether it is at-least-once, idempotent, or transactional.

### 9. Time, Windows, And Late Data Are Incomplete

**What Flink does**

Flink has event time, watermarks, tumbling/sliding/session windows, allowed
lateness, and side outputs for late data.

Official references:

- [Flink event time and watermarks](https://nightlies.apache.org/flink/flink-docs-stable/docs/dev/datastream/event-time/)
- [Flink windows](https://nightlies.apache.org/flink/flink-docs-stable/docs/dev/datastream/operators/windows/)

**What Wire does today**

Wire has watermark strategies and propagation primitives. The SDK exposes window
builders. But distributed keyed windows are not runnable because keyed shuffle
and managed state are missing.

**Recommendation for Wire**

Implement windows after keyed state works.

First window target:

- keyed tumbling event-time window,
- one aggregate/reduce function,
- watermark-triggered close,
- state cleanup after watermark,
- no allowed lateness initially.

Late data and side outputs should come after the basic window path is correct.

### 10. Production Operations Are Not Yet Credible

**What Flink does**

Flink has mature operational surfaces: job lifecycle APIs, checkpoint/savepoint
commands, high availability modes, metrics, Web UI, deployment modes, and
well-understood failure semantics.

**What Wire does today**

Wire has basic HTTP APIs, Prometheus metrics, and some coordinator recovery.
But several config surfaces are not wired end to end:

- HTTP TLS config exists but the HTTP server starts plain HTTP.
- node TLS config exists but coordinator/worker transport startup does not use
  it in the main path.
- auth config exists but there is no API auth middleware.
- CORS config exists but is not applied.

**Recommendation for Wire**

Before calling Wire production-ready:

- wire TLS into HTTP and node transport,
- add API auth middleware,
- expose checkpoint, task, backpressure, and per-operator metrics,
- add structured error responses and event logs,
- run the full test suite in CI with network permissions,
- run soak tests with worker kill/restart and checkpoint restore.

## Suggested Implementation Roadmap

### Milestone 0: Truthful Capability Boundary

Goal: make docs and validation match current reality.

Tasks:

- Add a current capability matrix to docs.
- Mark exactly-once, savepoints, Pebble state, windows, and recovery as target
  capabilities until they are wired through cluster mode.
- Reject unsupported cluster SDK graphs early with clear errors.
- Keep `docs/flink/` comparison docs current.

This prevents users from trusting guarantees the runtime cannot yet deliver.

### Milestone 1: Distributed Linear And Keyed Shuffle MVP

Goal: one distributed job actually moves data between workers.

Tasks:

- Split graphs at shuffle boundaries.
- Populate task upstream/downstream channel metadata.
- Implement worker-side task-to-task stream setup.
- Implement hash routing by key group.
- Support source, intermediate, and sink tasks.
- Add integration test: two workers, source on one, keyed downstream on another.

Acceptance test:

```text
SourceNamed -> MapNamed -> KeyBy -> SinkNamed
```

Records with the same key must always arrive at the same downstream subtask.

### Milestone 2: Managed State MVP

Goal: keyed state works in a distributed task.

Tasks:

- Implement Pebble state backend.
- Expose state through task context or process context.
- Encode keys by key group/operator/user key/namespace.
- Add state cleanup primitives.
- Add tests for checkpoint/restore and prefix iteration.

Acceptance test:

```text
Source -> KeyBy(user_id) -> CountPerUser -> Sink
```

The count must be correct under parallelism greater than 1.

### Milestone 3: Cluster Checkpointing

Goal: completed checkpoints are real and restorable.

Tasks:

- Coordinator checkpoint scheduler.
- Worker snapshot command handling.
- Barrier injection from source tasks.
- Barrier forwarding through task-to-task streams.
- Task ACK with state handles and source offsets.
- Persist checkpoint metadata.

Acceptance test:

Run a stateful job, trigger checkpoint, stop the job, restore from checkpoint,
and verify state continuity.

### Milestone 4: Failure Recovery

Goal: survive worker crash.

Tasks:

- Detect lost worker via heartbeat timeout.
- Mark assigned tasks failed.
- Transition job to restarting state.
- Cancel surviving tasks.
- Restore latest completed checkpoint.
- Redeploy all tasks.
- Reject stale status updates by epoch/attempt.

Acceptance test:

Kill one worker during a running stateful job. The job must resume from the last
checkpoint and produce correct state/output semantics.

### Milestone 5: Savepoints And Rescaling

Goal: planned operations work.

Tasks:

- Implement real savepoint trigger.
- Require stable operator IDs.
- Restore from savepoint.
- Reassign key groups when parallelism changes.
- Move state by key-group ranges.

Acceptance test:

Run with parallelism 2, take savepoint, restart with parallelism 4, verify keyed
state is redistributed and results remain correct.

### Milestone 6: Reference Connectors

Goal: Wire can process real external data.

Tasks:

- Define source offset checkpoint contract.
- Define sink delivery contract: at-least-once, idempotent, transactional.
- Implement one file connector and one streaming connector.
- Add connector fault-injection tests.

### Milestone 7: Production Hardening

Goal: cluster can be operated.

Tasks:

- TLS/mTLS in main runtime.
- API auth.
- Metrics for checkpoint age, backpressure, task restarts, records in/out,
  watermarks, and state size.
- CI with race tests and network integration tests.
- Soak tests and chaos tests.

## What Wire Should Not Copy From Flink Yet

Wire should not chase the full Flink surface area immediately.

Defer:

- SQL and Table API.
- CEP.
- Web UI.
- unaligned checkpoints.
- multiple concurrent checkpoints.
- broadcast state.
- dynamic rescaling.
- count/session/sliding windows beyond one basic window implementation.
- many connectors.
- Kubernetes operator.

The near-term goal should be correctness and simplicity, not feature breadth.

## Product Positioning Recommendation

Flink's strengths are breadth, maturity, connector ecosystem, SQL/Table API,
and battle-tested operations.

Wire should compete first on a narrower wedge:

- Go-native runtime.
- Single binary.
- Simple coordinator/worker deployment.
- Embedded mode for local tests.
- Deterministic stateful stream processing without JVM operational weight.
- Strong correctness story once checkpoint/restore is real.

That positioning only works if Wire is honest about its current limitations and
lands a correct distributed stateful core before expanding the API surface.

## Priority Summary

| Priority | Area | Why |
|----------|------|-----|
| P0 | Distributed keyed shuffle | Required for stateful distributed streaming |
| P0 | Pebble engine state backend | Required for durable managed state |
| P0 | Cluster checkpointing | Required for exactly-once state semantics |
| P0 | Failure recovery | Required for production correctness |
| P1 | Savepoints and restore | Required for upgrades and rescaling |
| P1 | Reference connectors | Required for real use cases |
| P1 | SDK/runtime alignment | Required for developer usability |
| P2 | Windows and late data | Important after keyed state exists |
| P2 | Security and operations | Required before production deployment |
| P3 | SQL, Web UI, advanced APIs | Useful later, not core yet |

## Source References

- Apache Flink, [Stateful Stream Processing](https://nightlies.apache.org/flink/flink-docs-release-1.20/docs/concepts/stateful-stream-processing/)
- Apache Flink, [Fault Tolerance](https://nightlies.apache.org/flink/flink-docs-stable/docs/learn-flink/fault_tolerance/)
- Apache Flink, [State Backends](https://nightlies.apache.org/flink/flink-docs-stable/docs/ops/state/state_backends/)
- Apache Flink, [Savepoints](https://nightlies.apache.org/flink/flink-docs-release-2.0/docs/ops/state/savepoints/)
- Apache Flink, [DataStream Connectors](https://nightlies.apache.org/flink/flink-docs-stable/docs/connectors/datastream/overview/)
- Apache Flink, [Event Time](https://nightlies.apache.org/flink/flink-docs-stable/docs/dev/datastream/event-time/)
- Apache Flink, [Windows](https://nightlies.apache.org/flink/flink-docs-stable/docs/dev/datastream/operators/windows/)
