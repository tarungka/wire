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
