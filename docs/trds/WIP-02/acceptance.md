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
| Configuration (§3.1) | Input/output/alignment sizes and DrainTimeout exist. Upload concurrency and configurable task Pebble compaction bounds need implementation/integration. |
| Container CPU limits (§3.2) | No automaxprocs import or dependency found. Verify Go runtime baseline and implement the documented behavior with explicit evidence. |
| Six observability metrics (§3.3) | No wire_task_* metric instrumentation found. Add task channel usage, output blocking time, owned goroutines, upload duration and alignment bytes with lifecycle cleanup. |
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
