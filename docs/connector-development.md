# Developing a Wire connector

Connectors are compiled into an application worker. Use `github.com/tarungka/wire/sdk`
for the public contracts and `sdk.WorkerRegistry` to bind class names to factories.
The coordinator stores a submitted graph; it does not load Go implementations.
Every worker eligible to run that graph needs the same registered classes.

## Source lifecycle and replay

Implement `sdk.Source`: `Open`, `ReadBatch`, `GenerateWatermark`, and `Close`.
Constructors and factories should validate configuration but leave resources
unopened. A factory is called for every deployed attempt and must return a fresh
instance. `WorkerTaskContext` gives the logical job, task, operator and subtask,
plus the attempt, epoch and deployment generation. Partition external input using
those logical identities; an attempt ID changes during recovery.

`ReadBatch` must honor its context. Return a nil slice only for permanent end of
input. Return a non-nil empty slice when temporarily idle, with a bounded wait to
avoid spinning. Source checkpoint requests are processed between dispatched
batches, so an indefinitely blocked read also blocks checkpoint progress. Do not
advance the source cursor past records returned to the runtime. Release local
resources in `Close`, including listeners and any helper goroutines.

Implement `sdk.CheckpointedSource` for restartable input. `Checkpoint(id)` returns
an owned, versioned serialization of the consumed cursor. The runtime captures it
at a batch boundary, together with the chain's operator state. `RestoreOffset`
must validate the serialization and reposition the external reader so the next
read returns the first record after that cursor. A source must retain or be able
to re-read the underlying data; serializing a number cannot recreate it.

By default, restoration follows `Open`. Sources that must restore before exposing
resources can implement `sdk.PreOpenCheckpointedSource`. Its
`RestoreOffsetBeforeOpen` hook may load and validate state, but resource
acquisition still belongs in `Open`. The runtime invokes one restore path per
attempt, not both. HTTP ingress uses this hook to restore its sequence before the
listener accepts requests. All state methods must reject malformed or
incompatible versions instead of silently starting from the beginning.

Configure watermark behavior explicitly with the SDK's watermark strategy API.
Do not assume that implementing `GenerateWatermark` alone selects a custom
strategy. See the [WIP-04 compatibility notes](wip-04-release-notes.md).

The built-in HTTP source acknowledges volatile queue acceptance. Its checkpoint
stores a consumed sequence; it has no durable ingress log or producer replay
protocol. HTTP senders needing recovery must retain records and coordinate a
durable application acknowledgement and replay mechanism themselves. An HTTP
200 or restored sequence alone is not an exactly-once guarantee.

## Sinks and batch errors

Implement `sdk.Sink`: `Open`, synchronous `Write`, and `Close`. An error from a
write must describe failed delivery; do not return success after dropping data.
A retryable transport error can be ambiguous if the receiver applied the write
but its reply was lost. Use external idempotency keys when repeated delivery is
possible. Resource cleanup in `Close` is not a substitute for a successful write.

Optional `sdk.BatchSink` adds synchronous `WriteBatch`. The runtime coalesces up
to 100 queued records and flushes partial batches when input is idle and before
checkpoint, watermark and end-of-input boundaries. It also flushes on orderly
shutdown. Do not retain the supplied slice after returning. The HTTP connector's
`BatchSize` can split a runtime batch into smaller requests; it is a maximum,
not a minimum fill threshold.

A batch error fails the task. The runtime cannot infer which records a partially
successful external request applied. A connector must document partial delivery
and use receiver idempotency or transactions as appropriate. When a record-level
retry, DLQ, drop or classifier policy is configured, the runtime calls `Write`
per record so the original event can be attributed correctly. Connector-internal
bounded request retries still apply. See [error handling](sdk/error_handling.md).

## Transactional sinks

Implement `sdk.TransactionalSink` only when the external system can preserve
prepared transactions across worker loss and can fence obsolete writers. The SDK
adapter preserves these hooks; it does not create those external guarantees.
Optional `BatchSink` is also supported, with buffered writes flushed before
`PreCommit`.

The contract is:

1. `Open` establishes local resources. `RestoreCheckpoint`, when present, decodes
   the saved prepared transaction identity; it does not decide to commit it.
2. `RecoverTransactions` establishes the external deployment-generation fence
   for the supplied job/task identity. Reject stale generations and resolve
   orphan transactions, preserving only the selected globally completed
   checkpoint. Repeated recovery calls must be safe.
3. The runtime commits the selected completed transaction, if any, and begins a
   new transaction before processing records. `BeginTransaction` creates an
   active transaction under the current writer's authority.
4. `PreCommit(checkpointID)` makes current writes durable but invisible.
   `Checkpoint` then serializes the prepared handle needed by a fresh instance.
5. Only the coordinator's completed decision authorizes `Commit`. It must be
   idempotent, including when the external commit succeeded but its reply was
   lost. Numeric checkpoint ordering alone is not proof of completion.
6. `Abort` discards an active transaction or follows an explicit abort decision.
   `Close` must preserve prepared transactions whose decisions are unknown.
   Never guess an abort decision merely because the worker is shutting down.

Scope external transaction identities by logical job and task as well as the
checkpoint ID. Every mutating call must enforce the current writer's fence, not
just startup. Respect cancellation in context-taking methods. Prepared state
must outlive the checkpoint/recovery interval. Wire does not provide atomic
visibility across independent external sinks.

Record-level retry/drop/DLQ policies are not permitted on transactional sinks;
a write can stage data before reporting an error. A named DLQ destination must
also be nontransactional. Validate these combinations before deployment.

## Registration and submission

Register a source with `WorkerRegistry.RegisterSource` and a sink with
`RegisterSink`. Factory signatures use only public SDK types. Configuration is
an opaque byte slice: choose a versioned encoding, validate all fields, and use
the same encoding in the submitting client. Return a descriptive error for
unknown versions, invalid partition settings or missing required fields.

Build graphs with `AddSourceNamed` and `AddSinkNamed`, using the exact class names
registered on the workers. The [registered-worker example](../sdk/examples/registered-worker/main.go)
shows public factories, `RunWorker`, remote submission and export for the job CLI.
It is a minimal execution example, not a replayable production connector.
The [file connector example](../sdk/examples/file-connector/README.md) adds a
versioned consumed cursor and content-hash validation, with replay tests and
public-only worker/submission code.
The [HTTP connector guide](../sdk/connectors/httpapi/README.md) shows typed HTTP
configuration encoders and registration through `sdk/connectors/httpapi/worker`.
YAML custom-connector binding remains part of WIP-19 and is not established by a
successful SDK submission.

Treat configuration bytes as potentially persisted job metadata. Do not assume
that encoding a credential encrypts or redacts it. Secret-reference resolution
and authenticated cluster submission are separate WIP-17/19 requirements.

## Acceptance checklist

A connector needs evidence for its external-system semantics, not only interface
compilation. At minimum verify:

- A fresh factory instance per attempt; failed initialization releases resources.
- Cancellation interrupts idle reads and blocked writes; `Close` joins helpers.
- Checkpoint/restore resumes after the consumed cursor without skipping data,
  including a fresh worker instance and malformed-state rejection.
- Full buffers apply bounded backpressure; capacity becomes available after drain.
- Ambiguous delivery and retries preserve idempotency keys, or explicitly expose
  at-least-once behavior. Permanent errors retain the original event for a DLQ.
- Batches flush before checkpoint preparation and completion, and a failed flush
  prevents success from being reported.
- Transactional sinks survive lost commit replies, fence old writers and reconcile
  prepared/orphan state without duplicate visible output.

Run race tests and integration tests through public worker registration. An
external-module build catches accidental dependencies on Wire's internal packages.
A timed developer trial is still needed to establish WIP-16's under-one-hour
onboarding target; this guide and existing examples alone do not prove that target.
