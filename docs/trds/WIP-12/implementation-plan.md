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
- Still required: named late streams and routing, Reduce/Apply, backend persistence
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
- Completion remains unproven: add named-window SDK deployment configuration,
  real worker/coordinator late-output checkpoint/recovery acceptance, invalid
  routing/configuration coverage, metric restore/purge integration and a full
  requirements/coverage audit. Then update final docs and publish the PR.
