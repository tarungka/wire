# WIP-14 completion checklist

The original scope remains the target. WIP-12 (#227) supplies window execution,
late output routing and durable window snapshots; this branch builds on it.
Do not mark WIP-14 implemented until each item has execution evidence.

- [ ] Graph fidelity: union, branching, connected streams, all shuffle forms,
  explicit per-node parallelism and timestamp assignment.
- [x] Per-instance source/sink construction and exactly one lifecycle per instance.
- [x] Process context: durable value/list/map state, typed helpers, TTL, event time,
  watermark, event-time timers and general named side outputs.
- [x] Checkpoint/restore of all managed state and timers; no cross-key/operator leakage.
- [x] Named distributed Process registration and keyed execution with the same APIs.
- [ ] Environment checkpoint/restart settings reach the runtime instead of being ignored.
  Interval, timeout, minimum pause and restart policies are wired; audit the
  original named configuration APIs and concurrent-checkpoint requirement.
- [x] MiniCluster and test harness exercise parallel state, timers, side outputs and recovery.
- [ ] Public API walkthrough and runnable examples, YAML schema delegation to WIP-19.
- [ ] Integration and race tests, build/vet/lint, one ready PR with acceptance evidence.

## Verified implementation checkpoint — 2026-09-24

Graph execution now preserves unions/branches and connected inputs; Broadcast
and Rebalance work in embedded and physical output routing. Explicit unequal
operator counts become Rebalance edges in SDK cluster conversion. Concrete
connectors have one lifecycle; factories create parallel instances. Timestamp
assignment is executed, and bounded embedded inputs publish final watermarks.
Per-input idle timeouts survive graph routing.

Managed state adds typed values and TTL, event-time context and timers, named
side outputs and a production-adapter harness. Invocation state is buffered and
committed atomically, preserving WIP-11 retry/drop/DLQ behavior without retaining
failed state changes. A two-worker test checkpoints Process state and pending
timers, fails the source, restores from replicas, verifies routed replay and
completes another checkpoint. The full repository race suite passes, as do
build, vet and lint. SDK tests additionally cover the final graph-conversion edit.

Next: implement MiniCluster's execution/recovery driver and carry environment
checkpoint/restart settings into cluster submission and scheduling. Existing
`SetCheckpointInterval` / `SetRestartStrategy` are still not runtime-complete.
Keep WIP-14 Partially Implemented until those gates and the public walkthrough
are verified; no completion PR has been created yet.

## Per-job checkpoint policy

SDK cluster submission now carries interval, timeout and minimum pause in the
graph. Submission validates and persists the policy with job metadata; legacy
graphs retain coordinator defaults. A joined coordinator runner schedules
periodic checkpoints outside the placement and heartbeat loops. Trigger times
are persisted atomically with checkpoint decisions, active checkpoints prevent
overlap, and savepoints/final checkpoints bypass minimum pause. Tests exercise
the actual runner, persisted metadata, timeout boundary and SDK HTTP envelope.
Restart strategies and the local MiniCluster driver remain pending.

## Per-job restart policy

Cluster submission now persists explicit fixed-delay, exponential-backoff and
no-restart policies. The delay applies before the first retry; exponential
delays saturate at the configured maximum. Explicit attempt budgets span the
job lifetime and survive coordinator recovery; legacy graphs retain the global
policy and stable-running reset. Requested rescales still bypass the recovery
budget, and failed rescale rollback remains subject to it. Policy validation
runs before SDK execution and coordinator persistence. Tests cover durable
policy round trips, first/second retry timing, exhaustion, no-restart, overflow,
invalid inputs, and the SDK HTTP submission envelope. Local execution still
needs the MiniCluster driver to apply these policies.

## Local coordinator/worker execution

MiniCluster now provisions loopback workers and independent replica storage,
registers SDK closures as worker factories, and submits the graph to the real
coordinator. Embedded environments with checkpointing or restarts enabled use
the same driver. Source offset restoration bridges CheckpointedSource into the
engine; factories construct replacement attempts. Shutdown cancels and joins
active executions. Local errors preserve Go error identities. Ordinary
embedded execution without recovery settings retains its fast executor.

The new acceptance scenario waits for a completed checkpoint, fails its source,
restores source offsets plus keyed state and a pending timer, then checks timer
and named side-output delivery at bounded completion. This exposed and fixed
periodic checkpoint starvation of final boundaries, and terminal watermarks
being lost between input tracking and the next periodic propagation tick.
Terminal watermarks now traverse the ordered data queue before final snapshots;
periodic emitters cannot regress them. Existing MiniCluster window/reduction,
DLQ lifecycle and retry tests run against production workers.

Local runtime metadata and replica storage are scoped to one Execute call, not
process-crash durability. Sources must support replayable offsets for checkpoint
recovery; ordinary sinks can see replayed records. Transactional sink correctness
still requires the WIP-10 sink contract. Public walkthrough and final scope audit
remain outstanding before a ready WIP-14 PR.
