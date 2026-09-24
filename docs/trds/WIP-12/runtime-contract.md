# WIP-12 runtime contract

## Event time and retained results

Window dimensions and allowed lateness use whole milliseconds. SDK duration
arguments must be nonnegative whole milliseconds; size, slide and gap must be
positive where required. Legacy integer AllowedLateness arguments remain
milliseconds. Lateness larger than the window is supported.

A record is late when its timestamp is below the operator's current watermark.
It remains eligible for every assigned window whose end plus allowed lateness
is greater than that watermark. Sliding records can update remaining windows
when other assigned windows have expired. Only when every assigned window has
expired is the original record routed once to the late stream or dropped.
Zero lateness removes state at window end; it does not reject a record that
still belongs to an open overlapping window.

Aggregate, Reduce and Apply execute for tumbling, sliding and session windows.
After an initial result, each accepted late arrival produces an updated result.
Session merges may extend bounds; old outputs are not retracted. Consumers that
need an upsert use the key and bounds, not arrival order alone. Apply receives
WindowInfo.IsUpdate. Every emitted result also carries the reserved headers
wire.window.start, wire.window.end and wire.window.update. sdk.DecodeWindowResult
reads these while leaving the result payload unchanged. Append-only sinks see
both initial and updated records.

## SDK, YAML and deployment

An embedded pipeline can call SetLateOutputTag on WindowedStream and then
GetSideOutput on its Aggregate/Reduce/Apply result. That view can be transformed
or sent to a separate sink. YAML window configuration accepts allowed_lateness
as a duration string and late_output as a name; another transform or sink can
use that name as its input. Names cannot collide with operators or __dlq__.

For cluster deployment, WindowedStream.ApplyNamed(name, class, config) selects a
worker.RegisterWindow factory. The factory supplies the aggregation, result
encoder and optional state backend; SDK dimensions and lateness are serialized
separately and applied before Open. EventTimeWindowOperator implements this
configuration interface. Existing descriptors without Window retain their
factory-defined dimensions. Arbitrary Go closures are not serialized.

Coordinator submission and worker deployment validate window definitions and
late-output types before execution. Record error policies on windows are not
supported: a result encoder or Apply callback can fail after state advanced,
so retry/drop/DLQ at record scope would be unsafe. Such failures require normal
task recovery. SDK error policies already exclude Window/Reduce nodes.

Upgrade coordinators and workers together before submitting tagged-output jobs.
Older binaries do not understand output groups or serialized window definitions;
this feature does not claim mixed-version routing compatibility.

## Ordered branches and checkpoints

Embedded window graphs preserve main and named late branches. Distributed plans
create separate output groups; data selects its group, while watermark, barrier
and end-of-partition fences reach every output stream. Late records bypass the
remaining operators in their producing physical chain and enter the late branch.
Checkpoint alignment still includes every upstream stream, including branches
that have not emitted data.

Window snapshots preserve accumulators, bounds, watermark and firing flags.
Workers restore these before processing replayed records. An unchanged fired
window does not fire again merely because its watermark is restored. A replayed
late record updates the restored accumulator. Ordinary sinks can observe replay;
exactly-once external visibility requires the transactional sink contract.
The known WIP-10 mixed bounded/unbounded source completion limitation is separate.

## Storage and resource bounds

Each window operator owns a backend namespace. Embedded windows use the selected
state backend. Worker factories may supply StateBackendFactory; the default
creates a private temporary Pebble backend. Local files are disposable: portable
checkpoint bytes are the recovery source.

Watermark, stats and changed per-key window records are published atomically.
Purge deletes retained records from the backend, not just from the working cache.
A store with window records but missing metadata is corrupt and cannot be opened
as empty state. Restore validates its snapshot before replacing live state.

The working cache remains in memory, bounded by 100,000 retained windows and
64 MiB of logical key/accumulator bytes by default. Factories can configure
MaxWindows and MaxStateBytes. These limits exclude Go object overhead, backend
indexes and encoded snapshot overhead. Apply retains input records within the
payload bound; Reduce keeps an incremental accumulator. Pebble persistence does
not imply unlimited disk-backed working memory. Checked aggregation errors and
rejected growth leave the prior processor state intact.

## Metrics

All four metrics carry operator and, in worker execution, task_id attributes.
wire_late_events_total counts arrivals below the watermark;
wire_late_events_allowed_total counts those accepted by any assigned window;
wire_late_events_dropped_total counts records rejected by every window, including
records delivered to a named late stream. It means expired, not necessarily lost.

wire_window_state_retention_bytes measures logical key and accumulator bytes in
closed-but-retained windows. It is neither total open state nor Pebble disk usage.
Purge updates the gauge, restore reconstructs it, and Close unregisters it.
Restoring historical processor counters does not emit those counts again; new
arrivals, including replay, are counted as runtime activity.
