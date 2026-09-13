# HTTP API connector

`httpapi.Source` receives JSON event envelopes at `/ingest`; `httpapi.Sink`
delivers the same envelope to an HTTP endpoint. Both satisfy the SDK interfaces
and engine operator interfaces. Configuration is supplied to constructors;
worker factories decode msgpack configuration structs.

```go
source, err := httpapi.NewSource(httpapi.SourceConfig{
    Address: ":8080",
    CertFile: "server.crt",
    KeyFile: "server.key",
    Auth: httpapi.Auth{Type: "bearer", Token: token},
    BufferSize: 10000,
    MaxBatch: 100,
})
// Handle err.
sink, err := httpapi.NewSink(httpapi.SinkConfig{
    URL: "https://receiver.example/events",
    Auth: httpapi.Auth{Type: "bearer", Token: token},
    BatchSize: 100,
})
// Handle err.
env := sdk.New()
env.AddSource(source).AddSink(sink)
_, err = env.Execute(ctx)
```

Use `AllowInsecure: true` explicitly for a plaintext HTTP listener or URL.
Source TLS certificates and sink HTTPS certificate validation are required by
default. Authentication supports bearer, basic, or none. The sink also accepts
custom headers. Redirects are returned as delivery errors rather than followed.

The request format is:

```json
{"events":[{"key":"user-123","value":"{\"action\":\"click\"}","event_time":1706000000000,"headers":{"source":"web"}}]}
```

Keys, values, and header values are UTF-8 strings, not base64. The sink rejects
non-UTF-8 bytes. Source requests must contain a single nonempty envelope with
known fields. Ingest is atomic per request: either every event fits in the
bounded queue or none are accepted and the server returns 429. Malformed input
returns 400; oversized bodies return 413. Successful responses report `accepted`
and the last assigned `sequence`.

`ReadBatch` blocks when idle and drains up to `MaxBatch` records. Use one reader
per source. `Close` unblocks it and stops the listener. Each source needs its own
listen address; the SDK example uses parallelism one.

`Write` sends one event synchronously. `WriteBatch` splits explicit batches at
`BatchSize`; the current engine does not automatically combine individual Write
calls. It retries network errors, 5xx, and 429 up to `MaxAttempts`, with constant
or exponential backoff. A 429's Retry-After is bounded by `MaxDelay`. Other
non-success statuses return a permanent `DeliveryError`. Errors are returned to
the runtime/caller; this connector does not automatically create or route a DLQ.

If `IdempotencyKeyField` is set, each event value must be a JSON object containing
that field. The sink hashes the ordered list of field values into
`X-Idempotency-Key`. IDs retain exact JSON numeric precision. Retries reuse the
same body, idempotency key, and random `X-Wire-Batch-ID`. A receiver must implement
deduplication; the grouping/order must remain stable for replayed batches. A
multi-request batch can be partly delivered before a later request fails.

## Offsets and guarantees

Source `Checkpoint` encodes the last **consumed**, not merely accepted, sequence
as eight big-endian bytes. `RestoreOffset` accepts that value before Open and
resumes sequence allocation. It does not recover queued events or request replay
from a sender. An ingest 200 acknowledges volatile acceptance, not durable
processing. Crashes can lose queued/in-flight events unless the sender retains
and replays them. There is no HTTP two-phase commit or exactly-once guarantee.

SDK `CheckpointedSource` lets adapters forward checkpoint data; automatic offset
restore through cluster recovery remains separate work. Worker factories are
available for custom worker registries (`SourceFactory`, `SinkFactory`); worker
source startup depends on the WIP-20 lifecycle fix in PR #188.

## Writing another connector

Implement SDK Source (`Open`, blocking `ReadBatch`, `GenerateWatermark`, `Close`)
or Sink (`Open`, synchronous `Write`, `Close`). Return an empty batch only for end
of input. Release resources on Close and honor context cancellation on blocking
I/O. Implement `CheckpointedSource` only when you can explain the offset and
replay contract; expose `BatchSink` when explicit batched delivery is supported.
Use the HTTP tests as examples of exercising auth, cancellation, backpressure,
retry identity, and end-to-end SDK lifecycle with local test servers.
