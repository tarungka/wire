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
- [ ] MiniCluster and test harness exercise parallel state, timers, side outputs and recovery.
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
