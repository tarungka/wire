# Error policies and dead-letter sinks

Attach a policy to the transformation or sink that can fail:

```go
stream.Map(parse).WithErrorHandler(sdk.ErrorHandler{
    MaxRetries: 3,
    Backoff: "exponential",
    InitialDelayMS: 100,
    MaxDelayMS: 10000,
    Multiplier: 2,
    OnExhausted: "dlq",
}).WithDLQSink(deadLetterSink)
```

The default action is `fail`. `drop` discards the record; `dlq` sends it to the
configured sink. Wrap `sdk.ErrTransient` to request retries. Wrap `sdk.ErrFatal`
to fail immediately regardless of policy. Standard network timeouts, connection
resets/refusals and broken pipes are transient; disk-full and allocation errors
are fatal. Other errors and processing panics
skip retries and use the exhausted action. Retries may repeat external side
effects, so operations must tolerate repeated execution.

Retry counts are 0–10000. Backoff is `none`, `fixed`, or `exponential`; delays
are nonnegative milliseconds. Exponential delay requires a positive initial
delay, a maximum at least as large, and a finite multiplier of at least one.
Invalid builder settings panic, matching other SDK configuration builders.
Sources and non-executable graph nodes do not accept this policy.

`WithDLQSink` accepts an inline SDK sink for embedded mode. For cluster mode,
use `WithDLQSinkNamed("registered-factory", configBytes)` after the policy.
The worker must register that sink factory. Inline sinks are rejected in
cluster mode; named sinks are rejected in embedded mode.

DLQ sinks open before processing and close after execution. Open errors or panics
fail execution before processing begins.
Cleanup is attempted even after a failed Open. Close errors and panics are logged
without failing the main job. Connector factories still fail configuration when
a requested destination cannot be constructed. Embedded parallel
instances share each configured sink; its Write method must support concurrent
calls. Use a distinct sink instance for each operator's DLQ. Writes are
synchronous and receive the execution context; sinks must honor cancellation.
Each record contains `original_event` (base64 key/value/header bytes), `error`,
`operator`, `timestamp`, and `retry_count`. No checkpoint transaction covers
DLQ delivery. Sink write failures or panics log an error and drop the record;
a missing DLQ destination is rejected during YAML/SDK/worker validation. DLQ delivery is best effort: records can be
lost when delivery fails and can be duplicated when a task replays input after
recovery. The main stream's checkpoint guarantees do not extend to DLQ output.

Only successful attempts publish Map/FlatMap output. Retried calls receive a
fresh copy of the original key, value and headers, so a failed attempt cannot
change a later retry or the DLQ envelope. This adds a payload copy per attempt
when an error policy is active. Operator state and external writes are not
rolled back; retryable operations must still tolerate repeated execution.
Transactional main sinks require `on_exhausted: fail` and `max_retries: 0`:
a Write may stage output before returning an error, so retry/drop/DLQ on that
sink can duplicate or commit a failed record. Recover the whole transaction
instead. Policies on upstream transformations remain supported. Transactional
sinks cannot be used as DLQ destinations; the DLQ path has no commit protocol.
Cancellation of the task bypasses Drop/DLQ handling and stops execution, including
a blocked DLQ write when the sink honors its context. Sink adapters preserve the
synchronous `Write` contract: a batch-capable sink can delegate Write to
WriteBatch (as the HTTP connector does), and errors propagate through the same
retry policy. The runtime does not create larger batches for a sink. HTTP
429/5xx delivery errors participate in operator retries after the connector's
own attempt limit; other HTTP response errors remain non-retryable.

## YAML

`ParsePipelineYAML` accepts the same policies with duration strings. Register
connector factories in `PipelineConnectors`; connector names below are examples,
not built-in implementations.

```yaml
apiVersion: wire/v1
kind: Pipeline
metadata:
  name: parse-with-dlq
spec:
  sources:
    - name: input
      type: my-source
  transforms:
    - name: parsed
      type: json-parse
      input: input
      config:
        target-field: payload
      error_handling:
        max_retries: 3
        backoff: exponential
        initial_delay: 100ms
        max_delay: 10s
        multiplier: 2
        on_exhausted: dlq
  sinks:
    - name: output
      type: my-sink
      input: parsed
    - name: dead-letters
      type: my-dlq-sink
      input: __dlq__
```

All policies are validated before connector factories run. Durations must be
nonnegative whole milliseconds. At most one sink may use `__dlq__`; it cannot
have its own error policy or feed the main graph. The name `__dlq__` is reserved.
A normal output sink is still required. The YAML destination is shared by all
operators with `on_exhausted: dlq`, opened once, closed once and receives
serialized writes. Existing YAML execution restrictions still apply.

## Metrics

The engine, worker and embedded SDK use the configured OpenTelemetry provider.
With no provider, the OTel default remains a no-op. A caller supplying engine
metrics explicitly can still select `NoopErrorMetrics()`.

- `wire_operator_errors_total{operator,error_type}` counts failed invocations,
  including failed retries. `error_type` is `transient`, `poison` or `fatal`;
  compatibility observations without a class use `unknown`.
- `wire_operator_retries_total{operator}` counts retry invocations.
- `wire_dlq_events_total{operator}` counts successful DLQ delivery or admission
  to an explicitly supplied DLQ channel.
- `wire_operator_drops_total{operator}` counts policy drops and failed, full or
  missing DLQ delivery.
- `wire_dlq_overflow_total{operator}` counts full-channel drops.

Distributed task counters also carry `task_id`. Task cancellation is not an
operator error. Raw error messages are never used as metric labels.
The old internal `TaskSlotConfig.DLQBufferSize` field is deprecated and ignored;
task destinations use the per-operator DLQ writer, without a log-only queue.
