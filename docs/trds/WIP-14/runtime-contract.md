# WIP-14 SDK runtime contract (work in progress)

This branch is not yet the completed WIP-14 deliverable. The remaining gates are
tracked in [implementation-plan.md](implementation-plan.md).

## Graph execution

Union creates an identity merge node; it never feeds one source into another.
Connected inputs keep separate CoMap/CoFlatMap functions before merging. Branched
graphs preserve every edge and copy mutable records between branches. SDK
Forward edges with unequal instance counts redistribute records; KeyBy computes
the key before its outgoing hash shuffle. Rebalance and Broadcast are supported;
a broadcast gives each embedded destination its own payload copy.

A concrete source or sink is one instance by default, even when the environment
parallelism is greater than one. To create parallel connectors, use
`AddSourceFactory` / `AddSinkFactory` and return a fresh unopened instance for
each `InstanceContext`. Each instance is opened and closed once. An explicit
parallelism greater than one on a concrete connector is rejected. Map/Process
functions may run concurrently across instances and must not mutate shared
closure state unsafely.

A closed embedded input contributes a terminal watermark, allowing bounded
pipelines to flush their final windows and event-time timers. This does not change
the distributed final-checkpoint protocol or resolve its mixed-source caveats.
Source timestamp extraction runs before watermark observation. Timestamp changes
on later streams are explicit map nodes and cannot retroactively change source
watermark generation. Cluster closures cannot be serialized: use registered
operators for timestamp assignment and other functions.

## Managed Process state and timers

`ProcessContext` provides value/list/map state, key, event time, watermark and
named side output emission. The adapter uses the selected state backend, scoped
to operator and instance; state names and user keys have length-delimited
namespaces. Storage errors fail the invocation before its output is emitted.
Typed value helpers encode int64/float64 in big-endian bytes and strings as bytes.
Invalid typed bytes return errors. Existing Get/Set APIs remain available.

`WithTTL` selects processing-time expiry refreshed only by writes. Value and list
state expire as a whole; map entries expire independently. Reads lazily remove
expired records. Value/expiry writes and deletion are atomic batches. Expiry is
absolute and included in snapshots, so restoring a checkpoint does not extend
retention. A write through `WithTTL(0)` disables expiry for that value. Negative
or overflowing TTL values fail processing. TTL does not describe window retention.

Use `ProcessWithTimers` and a TimerFunc to register event-time timers. A key and
timestamp identify one timer, including across checkpoint restore. Timer callbacks
run serially with records, see their registering key and triggering timestamp,
and can read state, emit records and register/delete timers. A timer registered
by a late record at or below the current watermark fires before that invocation
finishes; it does not wait for another watermark. A boundary permits up to 100,000
callback invocations; exceeding that limit fails the task rather than letting a
self-rescheduling callback wedge it. Checkpoints include timer registrations and
the last watermark as well as user state.

Declare Process tags using `WithSideOutputs` before attaching `GetSideOutput`
branches. An undeclared emission fails processing; declared but unconnected
outputs are discarded. Side outputs bypass subsequent main-path operators.
For cluster execution, use `ProcessNamed`, `worker.RegisterProcess` and a fresh
`sdk.NewProcessOperator` from each factory. Worker deployment supplies side-output
declarations and isolates explicit state directories by job/operator/instance.
Coordinator and workers must both understand the new Process tags and broadcast
output groups; upgrade them together before submitting these graphs.

Managed Process invocations buffer value/list/map/TTL/timer mutations and commit
one atomic batch only on success. An error or panic discards those mutations,
so record retry/drop/DLQ policies cannot double-apply managed state. This does not
roll back external side effects inside callbacks; those must be idempotent.
Timer errors during watermark handling fail the task and require checkpoint recovery.
The embedded executor does not yet provide the full checkpoint/restart driver;
that remains a WIP-14 completion gate, not an exactly-once claim.

## Test harness

`NewProcessHarness` uses the production managed-state/timer adapter with a hashmap
backend. It supports Process, AdvanceWatermark, SideOutput, Snapshot, Restore and
Close. SetProcessingTime provides a deterministic TTL clock. TestHarness.RunProcess
now retains keyed state across all its input records instead of resetting it for
each input.

Evidence includes the two-worker
`TestClusterProcessStateTimerAndSideOutputRecovery`: a checkpoint captures keyed
state and a pending timer; a source failure causes replica restore, and replay
produces the same state-dependent output and timer result through separate main
and side-output streams. A subsequent checkpoint completes across both branches.
Its ordinary sinks intentionally observe replay; this is not a transactional-sink
exactly-once claim.
