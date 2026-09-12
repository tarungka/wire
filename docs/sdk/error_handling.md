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
to fail immediately regardless of policy. Other errors and processing panics
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

DLQ sinks open before processing and close after execution. Embedded parallel
instances share each configured sink; its Write method must support concurrent
calls. Use a distinct sink instance for each operator's DLQ. Writes are
synchronous and receive the execution context; sinks must honor cancellation.
Each record contains `original_event` (base64 key/value/header bytes), `error`,
`operator`, `timestamp`, and `retry_count`. No checkpoint transaction covers
DLQ delivery. Sink write failures or panics log an error and drop the record;
a missing DLQ also logs and drops. The engine has drop counters, but runtime
metric export remains unfinished.

YAML error-policy parsing and the reserved `__dlq__` input remain proposed.
