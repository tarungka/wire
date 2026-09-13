# WIP-02 completion audit

Baseline: master `5d7ac4b` (includes merged WIP-01 PR #210). This follow-up
supersedes the limited barrier-identity change in #207 and initial #149.
Status remains Partially Implemented until all applicable items below have
implementation and executable evidence. The WIP-01 CRC latency waiver does not
waive any WIP-02 requirement.

## Acceptance scenarios (§8.1)

| # | Requirement | Current evidence / remaining work |
|---|---|---|
| 1 | Slow sink bounds source reading | Existing TaskSlot backpressure tests; audit network and output fan-out bounds. |
| 2 | Cancellation drains and joins within five seconds | Two-phase cancellation stops intake, releases alignment buffers, drains fetched batches/read-ahead through the chain, and bounds processing/output with DrainTimeout. Helpers are joined. Six regression scenarios pass 20 race-enabled repetitions; full race/integration suite and lint pass. Transactional cleanup and operator lifecycle/resource bounds still require audit. |
| 3 | Async checkpoint replication permits continued processing | TaskSlot now submits captured aligned snapshots to the bounded uploader. A real stream test verifies continued processing during blocked replication, delayed ACK, and EOF waiting for success or abort. Durable worker replica transport and source checkpoint injection remain required. |
| 4 | Concurrent source read/watermark safety | Separate source reader and legacy watermark strategy exist; audit source implementations and add full-lifecycle race evidence. |
| 5 | Two-input alignment / snapshot / release | WIP-01 ordering and atomic buffer transfer regressions exist. Retain pre-barrier snapshot and barrier-before-post-data ordering. |
| 6 | Abort drains without snapshot | Existing abort tests; audit races with upload completion and shutdown. |
| 7 | Operator panic fails task and joins siblings | Panic recovery and authoritative chain-error handling exist; validate worker FAILED reporting and lifecycle cleanup. |
| 8 | Atomic watermarks | Tracker CAS and emitter concurrency tests exist; audit end-to-end publication semantics. |
| 9 | Prompt control mailbox handling under full data channels | Control-priority test exists; audit blocking output and checkpoint-work interactions. |

## Other explicit requirements

| Requirement | Current evidence / remaining work |
|---|---|
| Per-task topology and bounded channels (§2.1–2.4) | Input reader includes one bounded read-ahead helper. Output uses one ordered dispatcher, not the stated per-output workers. Reconcile topology with ordering and test bounds. |
| Replication failure affects checkpoint, threshold affects task (§2.7) | Coordinator failure reporting now applies existing checkpoint thresholds and ignores stale checkpoint/epoch reports; timeout metrics exclude replication failures. TaskSlot completion delivery now feeds this policy; durable worker replication and additional transactional/abort overlap tests remain required. |
| Configuration (§3.1) | Input/output/alignment sizes and DrainTimeout exist. Engine upload concurrency is implemented. Pebble compaction concurrency defaults to two and is configurable in engine/embedded SDK, retained across restore. Worker configuration integration remains open. |
| Container CPU limits (§3.2) | cmd/main.go imports automaxprocs v1.6.0 at startup. Explicit GOMAXPROCS takes precedence; quota rounding/minimum and restart behavior are documented. Command package builds locally; Linux cgroup execution remains to be verified (local Docker daemon is stopped). |
| Six observability metrics (§3.3) | Task input/output channel usage and alignment payload-byte gauges are registered in TaskSlot.Run and unregistered on exit. Upload duration is recorded around replication. Operator output blocking time is recorded on data/barrier/EOP sends. Engine-owned goroutines and callbacks are counted at entry/exit. Prometheus HTTP-handler export and gauge cleanup are tested; running-worker topology validation remains open. |
| Benchmarks (§8) | Engine benchmarks exist. Establish and record concurrency/channel/deserialization baselines after implementation. |
| Documentation and PR | Update actual topology, configuration and status only after validation; linked follow-up PR to #207/#149, using personal GitHub account. |

## Specification correction carried forward from WIP-01

The §2.5 diagram still puts side-buffer draining before barrier forwarding.
Those records arrived after the barrier and must remain after it. The WIP-01
implementation and regression preserve the correct ordering; WIP-02 must not
reintroduce the old behavior while implementing asynchronous snapshot work.

## Shutdown implementation evidence

`TestInputReaderDrainsReadAheadAfterIntakeCancellation` fills the bounded
read-ahead queue before cancelling intake, then verifies every record arrives.
`TestSourceReaderDrainsFetchedBatchAfterIntakeCancellation` preserves the rest
of a fetched batch while preventing another fetch. Task-level tests cover a
blocked first operator, a resumed downstream, and a permanently blocked
receiver. `TestDrainReleasesAlignmentWithoutReordering` verifies pre-barrier
records precede both buffered and blocked post-barrier records during shutdown,
and that alignment cannot restart once draining begins.

Prepared transactional sinks follow abort cleanup on cancellation; they cannot
accept more records while awaiting a global decision. This path still needs
explicit shutdown-budget validation. The implementation does not claim it can
forcibly terminate arbitrary user callbacks that ignore cancellation.

## Checkpoint upload lifecycle design

`TaskCheckpoint` identifies the task, checkpoint and epoch, and carries ordered
operator snapshot bytes. `CheckpointReplicator.Replicate` must return success
only after the configured durability requirement is met. An adapter must
replicate referenced state artifacts too; copying a local snapshot path is not
durable replication.

The task-owned uploader admits at most the configured number of active uploads
plus unconsumed completions. It has no idle worker goroutines and no pending
snapshot queue. Admission returns a busy result without waiting for I/O; the
runtime must delay or reject a new checkpoint without blocking data processing.
Snapshot bytes are copied before returning control to operators. Completion
carries checkpoint/epoch identity so stale results can be rejected. Close stops
admission, cancels uploads, and joins workers. Replicators must honor context
cancellation. Replication errors and panics become checkpoint result errors;
the task integration must apply WIP-05 failure thresholds rather than treating
every failed upload as a fatal task error.

These component invariants pass 50 race-enabled repetitions. Chain submission,
completion/abort fencing and terminal-task handling are connected as described
below. Durable replica transport remains open. No successful checkpoint may be
acknowledged before replication succeeds.

### Replication failure policy

`CheckpointCoordinator.FailCheckpoint` uses a bounded, cancellable mailbox.
The coordinator compares checkpoint ID and epoch under the state lock before
aborting, applies its consecutive-failure/rate limits, and sends normal abort
notifications. A timer captured for an earlier checkpoint is also checked
against its original identity. Replication errors do not increment the timeout
counter. Tests prove that an initial upload failure aborts only its checkpoint,
the configured failure threshold is enforced, and stale ID/epoch failures do
not change an active newer checkpoint. The uploader-to-runtime completion path is now connected; these tests still do not prove worker checkpoint durability without a durable replica adapter.

### TaskSlot checkpoint runtime connection

TaskSlot accepts a CheckpointReplicator and requires a coordinator when it is
configured. The operator chain captures snapshot bytes at alignment, submits
without waiting for replication I/O, and continues data processing. Transactional
ACKs are withheld until upload success. Replicated ACKs include epoch and wait
for coordinator application, avoiding task-EOF cancellation losing an ACK.
Nonfatal upload failures retain pending checkpoint state until abort handling;
EOF waits for that outcome. Abort cancels the matching upload and stale results
are ignored. Per-upload timeout follows checkpoint timeout; concurrency defaults
to one. The coordinator stays alive during the processing drain.

The network TaskSlot success/failure-at-EOF scenarios pass 30 race-enabled
repetitions. This validates runtime behavior with an injected replicator, not
peer durability. Worker configuration/replica transport, source checkpoint
injection, additional transactional cases and metrics remain open.

### Replica storage component

`FileCheckpointStore` persists inline operator bytes under a hashed
job/task/checkpoint/epoch identity. Publication uses a synced temporary file,
an atomic hard link that cannot replace an existing checkpoint, removal of the
temporary link, and directory sync before success. Identical retries are
idempotent; conflicting contents fail. Files carry a version, bounded payload
length (64 MiB encoded), and SHA-256 checksum. Reads validate the envelope before
allocating payload memory and verify the stored identity. The root directory
must already exist and be durable.

Twenty race-enabled repetitions cover concurrent identical and conflicting
publication, reopening the store, job isolation, cancellation, checksum
corruption, and malformed/truncated/oversized envelopes. Reopening is recovery
evidence, not a simulated power-loss test. Crashes before publication may leave
unreferenced staging files; startup cleanup remains part of worker integration.
This component is not a remote replica adapter: peer transport and transfer of
referenced Pebble files are still required before worker durability is complete.

### Pebble concurrency configuration

`StateBackendConfig.PebbleMaxCompactionConcurrency` defaults to two when zero;
negative values fail before creating storage. Both initial database open and
restored generations pass the limit to Pebble. The embedded SDK exposes
`StateBackendConfig.MaxCompactionConcurrency` and forwards it for each operator
instance. Tests inspect Pebble's saved OPTIONS after creation, restore and
reopen, and verify the SDK path reaches the database. This covers backend and
embedded execution configuration; the worker configuration path is still open.

### Task channel metrics

TaskSlot registers `wire_task_input_channel_usage` and
`wire_task_output_channel_usage` through the existing observability meter, so
worker tasks use the configured exporter. Scrapes read current bounded channel
lengths without polling goroutines. Each observation carries `task_id`;
unregistration on Run exit releases the callback and its channel references.
The manual-reader test verifies occupancy changes, identity, and absence of
observations after unregistering. Prometheus HTTP-handler verification is described below; running-worker
checkpoint integration remains required.

Checkpoint uploads record `wire_task_checkpoint_upload_duration_ms` around the
replicator invocation, including error/panic recovery and cancellation. Capture
and completion delivery are excluded. The histogram uses millisecond boundaries
through ten minutes; existing second-based latency views only match instruments
with unit `s`. A manual-reader test verifies fractional milliseconds, cancelled
contexts and histogram bounds. This instrumentation is exercised by uploader
tests, but worker peer replication remains unconnected.

`wire_task_alignment_buffer_bytes` counts logical key/value/header bytes plus
eight bytes per timestamp under the aligner lock. It excludes Go allocation and
container overhead. Both buffer entry paths increment it; finish, drain, reset
and shutdown clear it as ownership transfers. Retained event arrays are cleared
when reused so released payloads are not kept alive. Tests verify admission
limits, byte accounting, preserved transferred records, released references,
and metric observation/unregistration.


Operator output sends record `wire_task_backpressure_time_ms` only when the
initial nonblocking attempt cannot send. Data, barrier and EOP sends share this
path; cancelled waits are included, and ready sends do not read the clock.
TaskSlot attaches its task-labelled recorder to the chain context. The unit
test checks that ready sends produce no sample and that a full-channel wait
ended by a deadline records time without emitting a message. Prometheus counter export and unit conversion are covered below.


`wire_task_goroutine_count` counts running engine-owned task workers, read-ahead
helpers, upload workers and explicit shutdown callbacks. It excludes the caller
running TaskSlot.Run, shared transport goroutines, runtime timers and Pebble's
internal workers. Counting starts at goroutine entry, not admission. A blocked
upload test verifies zero while idle, one while replicating, and zero after
Close joins cancellation. Full running-task topology verification remains
required; this counter does not claim process-wide or Pebble attribution.


`TestTaskPrometheusExportAndCleanup` scrapes the Prometheus HTTP handler backed
by the production OTel exporter and an isolated registry. It verifies all six
metric families, queue/alignment/goroutine values, millisecond counter and
histogram values, and absence of live-task gauges after callback cleanup.
Prometheus exports the monotonic counter as
`wire_task_backpressure_time_ms_total`; histogram samples use the usual
`_bucket`, `_sum`, and `_count` suffixes. Cumulative counter/histogram series
remain exporter history after task exit; only live gauges are removed. This
is an exporter integration test with controlled values, not evidence of a
worker performing durable checkpoint replication.

## Worker checkpoint integration audit

The full `go test -race -tags=integration ./...` suite passes at `4d5029d`.
That verifies the accumulated branch changes, but existing tests do not close
the following runtime gaps found by tracing the command path:

- `worker.handleCommands` logs `CommandTypeTakeSnapshot` as a stub. Workers
  receive commands through WatchCommands/heartbeat; they do not currently run
  a checkpoint RPC server. Merely registering a TriggerCheckpoint handler on
  an unused server would not connect the runtime.
- `Coordinator.TriggerSavepoint` persists an in-progress entry but still has
  a barrier-injection TODO. TriggerCheckpoint/AcknowledgeCheckpoint message
  types and client methods exist without corresponding coordinator/worker
  checkpoint dispatch integration.
- The source reader has no checkpoint rendezvous. A control message alone is
  insufficient: it can overtake queued source records while another ReadBatch
  advances the source offset. The source must reach a batch boundary, stop
  fetching, and remain stopped until the chain drains pre-barrier records and
  captures the source/chain snapshot. Replication then runs asynchronously and
  fetching resumes; source snapshot capture must not race ReadBatch.
- Worker task construction does not set CheckpointReplicator or Coordinator.
  A per-task local coordinator cannot substitute for the job-wide decision:
  durable ACKs must be fenced by job, task, checkpoint and coordinator epoch,
  with global commit/abort sent to every participating task.
- Local inline snapshot storage cannot acknowledge Pebble manifests as durable
  remote replicas. Transfer referenced immutable files, verify them on the
  receiving worker, and make recovery independent of the originating worker's
  paths before publishing the manifest.

The next implementation must connect these existing command and ACK paths,
with source ordering and cancellation tests, followed by multi-worker replica
loss/recovery evidence. These are required work, not deferred WIP-02 scope.


### Source checkpoint boundary implementation

TaskSlot now accepts source-only CheckpointTriggers when a replicator and
coordinator are configured. The source reader checks requests between fully
dispatched batches, copies source checkpoint bytes, marks the virtual input's
barrier, and waits for the chain to release the boundary before another read.
The chain drains preceding records and captures operator state as before;
TaskCheckpoint carries source bytes separately from operator positions. The
uploader owns a copy of both, and local storage includes source bytes in its
size limit. No worker command is connected yet.

The boundary test requests a checkpoint midway through dispatching a two-record
batch and verifies both records precede the boundary, source state reflects one
fetched batch, and the next batch remains blocked until release. Further task
replication/abort/EOF overlap tests and coordinator epoch fencing are required
before this path is considered complete.

Source trigger redelivery is fenced within a running reader: duplicate IDs,
lower IDs in the accepted epoch, and older epochs are ignored before snapshot
capture. This prevents heartbeat/push redelivery from capturing advanced source
state under an existing identity. Ten race-enabled boundary/identity test runs
pass. This is not a replacement for worker assignment-epoch validation, which
must reject unexpected future epochs as well as commands from old leaders.

`TestSourceTaskReplicatesBoundaryAndContinues` now exercises the source path
through TaskSlot and real transport streams. A trigger created during the first
batch captures source offset one and keeps operator state separately. The
receiver observes the first record, checkpoint barrier, then the next batch's
record while the injected replicator remains blocked. No ACK or terminal task
completion occurs before the upload resolves. Success completes checkpoint 7;
replica failure aborts the checkpoint and permits normal EOF. Both cases pass
ten race-enabled repetitions. Remote replica persistence, command dispatch,
global decisions and recovery remain required.

### RPC cancellation prerequisite for peer transfer

RPC clients now bound unresolved stream opens to one per client and clean up
late streams after cancellation without closing the shared session. Unary
method timeouts cover opening as well as I/O. Cancellation expires the affected
stream's deadline before closing it, because Yamux half-close alone does not
wake a blocked read. Streaming calls clean up on parent cancellation and reader
exit, and error delivery respects cancellation under a full result channel.
The RPC race suite passes; ten cancellation regression runs verify a blocked
call exits and a sibling call on the same session still succeeds. Saturated
stream-open coverage now fills the real Yamux SYN backlog, cancels the blocked
open, verifies ten subsequent callers wait for the occupied slot, then accepts
a stream and verifies late-open cleanup releases the slot while an existing
stream still transfers data. Five race-enabled repetitions pass.
The existing 16 MiB RPC frame limit also means replica transfer must be chunked
rather than sending the store's entire 64 MiB snapshot in one unary payload.

### Checkpoint chunk framing

RPC method 0x0009 (ReplicateCheckpoint) is reserved for replica transfer.
`WriteCheckpointChunks` emits at most 1 MiB of snapshot data per RPC frame,
prefixed by an eight-byte offset. `ReadCheckpointChunks` enforces method and
request identity, contiguous offsets, nonempty chunks, declared total length,
per-frame allocation bounds and the transfer SHA-256 digest. Bytes go to private
staging storage; any failure requires discarding staging. The caller must check
the declared size against its quota and own cancellation of blocked transport
I/O. No worker endpoint or durable acknowledgement is connected yet.

Tests transfer a snapshot larger than the 16 MiB unary limit, and reject wrong
method/request IDs, gaps, empty chunks, excess data and checksum corruption.
These validate framing, not publication, peer identity or recovery. The transfer
request envelope, receiver admission, durable publication and client/server
integration remain required.

`Client.ReplicateCheckpoint` now sends a validated transfer envelope followed
by chunks on one cancellable stream. It waits for a receipt whose request ID,
method, stored flag, and complete snapshot identity/size/checksum match. A
missing caller deadline uses the ten-minute checkpoint budget. The receipt
contract requires the receiver to publish durably before setting Stored;
referenced artifacts must also be recoverable. Real Yamux tests verify that
finishing the body does not complete the client and reject negative, wrong-epoch
and wrong-request receipts. The RPC race suite and lint pass. The test receiver
discards bytes and simulates receipts; durable receiver integration remains open.
