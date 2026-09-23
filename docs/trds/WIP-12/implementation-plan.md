# WIP-12 completion plan

Base: master `931d02e` (includes WIP-10 and WIP-11). Follow-up to #193.

Completion requires all of the following; the existing processor tests alone
are not evidence of end-to-end completion.

1. Preserve event-time ordering through window execution, for tumbling, sliding
   and session windows. Support Aggregate, Reduce and Apply; expose window bounds
   and the update flag without discarding the aggregate payload.
2. Validate per-window allowed lateness in SDK and YAML, including explicit units,
   zero lateness and lateness larger than the window. Preserve compatible legacy
   millisecond SDK calls.
3. Implement named late-output streams in SDK, YAML, embedded execution and worker
   routing. Deliver an original event once only when every assigned window has
   expired. Carry watermarks, checkpoints and end-of-partition to both outputs.
4. Export the four WIP metrics with operator/task attribution. Retention bytes
   mean logical payload bytes for closed-but-retained windows, not all state or
   Pebble disk usage. Verify collection, purge, restore and lifecycle cleanup.
5. Persist window accumulators, watermark, bounds and firing flags with the
   configured state backend. Verify actual Pebble cleanup at retention deadlines
   and portable checkpoint restoration before replay in worker execution.
6. Exercise all five named acceptance scenarios across all three window types,
   plus session merges, partial sliding expiration, repeated updates, negative
   timestamps, overflow, corrupt snapshots and bounded state growth. Measure
   coverage against the WIP's 100% window semantics target and report any gap.
7. Update the WIP/index, runtime contract and named acceptance evidence only after
   the requirements above are verified. Run full race suite, build, vet and pinned
   lint; open a personal-account follow-up PR and inspect its checks.

The known WIP-10 mixed-source final-checkpoint hang is not addressed by this WIP.
Acceptance must distinguish that existing limitation from window recovery proof.

## Progress — 2026-09-23

- Added transport-preserved result bounds/update headers and SDK decoding without
  replacing aggregate payloads. Regression checks initial and late-updated count
  results through Event/record conversion and rejects malformed metadata.
- AllowedLateness accepts duration arguments while preserving integer-millisecond
  calls. YAML window configs accept validated whole-millisecond duration strings,
  including zero and values larger than the window.
- Added live late/allowed/expired counters and a closed-retention payload gauge,
  with window/task identities attached by embedded and worker runtimes. Gauge
  registration is released on Close. Targeted window/SDK/observability race tests
  and affected-package lint pass.
- At this increment, remaining work was named late streams and routing, Reduce/Apply, backend persistence
  and worker recovery acceptance, full requirement coverage and PR publication.

### Runtime and routing increment

- Reduce now uses an incremental checked accumulator; rejected reductions leave
  state intact. Apply retains input records under a logical payload limit.
  Window payloads default to a 64 MiB bound; window count remains bounded too.
- Window state is cached in memory within those limits and atomically persisted
  as per-key records plus watermark/config/stats metadata. Built-in Pebble and
  hashmap backends support the atomic batches. Embedded windows use the selected
  state backend; worker window factories can supply a backend factory, otherwise
  Open creates private temporary Pebble state. Portable checkpoint bytes do not
  depend on that local directory.
- Actual Pebble reopen, exact purge deletion, portable restore/replay and atomic
  backend-failure behavior pass for tumbling/sliding/session windows.
- SDK OutputTag/GetSideOutput now preserve tagged graph edges. YAML late_output
  names resolve to the producing window. Embedded window graphs retain branches
  instead of flattening topological order. Worker task plans carry output groups;
  data selects its group while barrier/watermark/end fences visit every stream.
- All nine MiniCluster combinations of Aggregate/Reduce/Apply and three window
  types pass initial result -> late update -> purge -> one original late record.
  YAML execution and network grouped-fence tests pass. Full repository race
  suite, build, vet and pinned lint passed for this increment.
- At this increment, remaining work was named-window SDK deployment configuration,
  real worker/coordinator late-output checkpoint/recovery acceptance, invalid
  routing/configuration coverage, metric restore/purge integration and a full
  requirements/coverage audit, final documentation and PR publication.

### Deployment and recovery acceptance increment

- Added SDK ApplyNamed for worker-registered window factories. Window dimensions
  and allowed lateness travel separately from factory configuration; SDK msgpack
  round-trip tests cover all three assigners and the named late edge. Worker
  configuration preserves factory aggregation identity, limits and backend.
- Coordinator submission and deployment reject malformed window definitions and
  unresolved late edge tags. An already opened, processed or restored operator
  cannot be reconfigured, even when its retained state is empty.
- Real coordinator/two-worker checkpoint replication and recovery now cover all
  three window types with separate main/late sinks. Each case checkpoints a fired
  window, updates it, purges it, routes one original expired record, injects a
  source failure, restores and replays. A second completed checkpoint verifies
  both output branches participate. Ordinary sink replay is explicitly not an
  exactly-once visibility claim. These tests passed with the race detector.
- Runtime OTel collection covers zero and nonzero lateness for all three window
  types, attributed counters, Pebble-backed retention, purge, close/unregister,
  checkpoint restore and suppression of historical counter re-emission.
- WindowProcessor and checked aggregator helpers now have 100% statement
  coverage under the window unit suite, including callback failure atomicity,
  timestamp extremes, saturated retention deadlines and 100 late updates.
  This is the core late-detection/retention/purge target, not whole-package or
  snapshot/storage coverage. Remaining work: final runtime/storage audit,
  acceptance documentation, full final verification and completion PR.

### Final acceptance audit

The [acceptance record](acceptance.md) maps all five proposal scenarios and the
configuration, routing, metrics, durable state and edge-case requirements to
executed tests. The [runtime contract](runtime-contract.md) documents deployment
configuration, resource bounds, upgrade ordering and replay visibility. Apply's
WindowInfo.IsUpdate is asserted inside the callback as well as on result headers.
The WIP and canon/index documentation now describe the implemented behavior.
Final local race suite, build, vet and pinned lint passed.
[PR #227](https://github.com/tarungka/wire/pull/227) is published from the personal
account and ready for review. Remote check results remain authoritative on the PR.
