# WIP-16 completion audit

This branch retains the original HTTP-focused proposal and is not a completion
claim. It builds on the public worker runtime and recovery work in WIP-14/15.

| Requirement | Evidence | Remaining work |
| --- | --- | --- |
| Source lifecycle, offsets, replay and watermarks | Source/CheckpointedSource, HTTP consumed sequence, existing HTTP tests | Real worker restart/offset acceptance; document sender-owned HTTP replay without claiming durable ingress |
| Sink batching | BatchSink and explicit HTTP WriteBatch | Automatic runtime batching, checkpoint/end-of-input flush and error handling |
| Transactional sink integration | Public TransactionalSink and worker adapters | Connector contract/recovery acceptance and development guide |
| Connector registration | Internal factories and new public HTTP worker factories | Live public-registry cluster example and YAML integration with WIP-19 |
| HTTP ingress/delivery | Existing auth/TLS, bounded ingress, retry/idempotency tests | Source/restore cluster acceptance; permanent delivery failure to a named DLQ now has a real-worker test |
| Custom connector guide | Existing connector README | Executable public-only example and recorded developer trial against the under-one-hour target |
| Quality gates | Existing HTTP tests | Final race suite, coverage against the 90% target, vet/lint, evidence for each numbered WIP scenario |

## Public worker registration

`sdk/connectors/httpapi/worker` provides public SourceFactory/SinkFactory and
Register helpers plus typed config encoders. Applications can use these with
`sdk.WorkerRegistry`, AddSourceNamed/AddSinkNamed and RunWorker without importing
Wire's internal worker/protocol types. Factories retain source checkpoint and sink
batch contracts, create fresh instances and do not open listeners during graph
construction. HTTP configuration bytes can contain credentials; secret references
and safe persistence remain part of the WIP-17/19 integration requirements.

## Public worker HTTP delivery acceptance

`TestPublicHTTPWorkerDeliveryAndNamedDLQ` starts real coordinator HTTP/RPC
services and `sdk.RunWorker` with public HTTP factory registration. Named graph
submission completes for successful delivery, a 503 followed by success, and a
permanent 400 routed to a registered DLQ. The test verifies request counts, stable
retry body/idempotency key, the original DLQ event, and no DLQ output on success.
These scenarios pass with the race detector. They establish the sink integration;
HTTP source restore/replay, runtime batching, YAML and developer-trial requirements
remain open.

## HTTP source restore ordering

The engine normally restores operators after Open. HTTP ingress requires the
sequence offset before its listener becomes visible, so the runtime now supports
an explicit SourceOffsetRestorerBeforeOpen contract. SDK PreOpenCheckpointedSource
adapters preserve that opt-in in embedded, local-cluster and registered-worker
paths. Other sources/operators retain their existing restore-after-open order.
Pre-open restore only loads state; connectors must acquire resources in Open.

A production TaskSlot test restores HTTP offset 42 and verifies its first ingress
request receives sequence 43. Invalid topology/offset tests verify no listener is
opened. `TestPublicHTTPSourcePauseResumeRestoresSequence` exercises a real
coordinator, two public workers and named HTTP source registration: ingress sequence
1 is delivered, an idle source is savepointed and paused, and a fresh instance
resumes with sequence 2. The test originally timed out because idle ReadBatch
never yielded to checkpoint handling. HTTP idle reads now return a non-nil empty
batch every 100ms, allowing the runtime to service boundaries without new traffic.
Both HTTP connector packages pass with the race detector. Engine, worker and SDK
race suites also pass for the restore-order change.

HTTP accepted events are still volatile, and restoring a sequence does not
recreate the input queue or request sender replay. Crash/replay acceptance and the
remaining scope above are not established by this pause/resume test.

## Runtime sink batching

The chain preserves the optional SDK BatchSink capability and coalesces up to
100 queued records. It flushes on idle input and before checkpoint snapshots /
transaction PreCommit, ordered watermarks, end-of-input and orderly shutdown.
No background writer owns data past a checkpoint boundary. Records are copied
when buffered. Sparse input is flushed without waiting for another record.

Batch failures fail the task before reporting the boundary; connectors remain
responsible for external partial-delivery/idempotency semantics. Configured
per-record retry, DLQ, drop or classification policies intentionally use Write,
since the existing batch error contract does not identify failed records. HTTP's
own bounded request retries still apply to each batch. HTTP BatchSize can split
a runtime batch into smaller requests; it does not force a minimum fill level.

Engine regressions cover bounds, ordering, partial flush, payload ownership,
boundary failures and per-record policy behavior. The SDK HTTP runtime acceptance
expands one record to 205 and checks ordered requests of 100, 100 and 5 records.
