# Writing and recovering an SDK job

Wire's Go SDK builds a graph, then runs it locally or submits named operators to
a coordinator. The complete runnable example is
[sdk/examples/stateful/main.go](../sdk/examples/stateful/main.go). Its
[test](../sdk/examples/stateful/main_test.go) executes both commands' pipeline
logic and checks the output. Use the Go version in `go.mod`.

From the repository root:

```sh
go run ./sdk/examples/stateful
go run ./sdk/examples/stateful -recover
go test -race ./sdk/examples/stateful
```

The last output line in normal mode is:

```json
{"main":["customer-a: count=1","customer-a: count=2"],"timers":["customer-a: timer=10 count=2"],"source_attempts":1}
```

Recovery mode produces the same records with `source_attempts` equal to `2`.
Runtime diagnostics may precede the result. No separately running coordinator or
worker is required; MiniCluster creates loopback listeners and temporary replica
storage, then joins its workers and removes that storage when execution ends.

## Build the graph

Start with `sdk.New()` (also named `sdk.NewStreamExecutionEnvironment()`) for a
lightweight embedded job, or get an environment from MiniCluster to exercise the
coordinator/worker execution path:

```go
cluster := sdk.NewMiniCluster(sdk.MiniClusterConfig{NumTaskSlots: 2})
defer cluster.Shutdown()
env := cluster.GetExecutionEnvironment()
```

`NumTaskSlots` is each local worker's capacity and the environment's default
parallelism. MiniCluster provisions enough workers for the graph, with at least
two workers for checkpoint replicas. Set operator parallelism explicitly where
the input has a different partition count.

The example creates one source instance, computes a key, and runs keyed Process
instances at the environment's parallelism. A concrete source/sink is one
instance by default. Use `AddSourceFactory` and `AddSinkFactory` to construct
fresh instances for parallel execution and replacement attempts; their
`InstanceContext` supplies the index and parallelism. Factories construct
connectors, while the runtime owns their `Open` and `Close` calls.

Map, FlatMap, Filter, KeyBy and Process accept Go functions. Named variants such
as `MapWithName` keep operator identity readable. `Union` merges inputs without
changing their upstream computations; `Connect` gives each input its own
CoMap/CoFlatMap function. Branches can feed separate sinks. Functions on parallel
instances can run concurrently, so shared closure data needs synchronization.

## Keep state and emit timer results

Inside Process, `c.GetState("purchase-count")` is scoped to the current key and
operator. The example reads with `ValueInt64` and writes with `SetInt64`. List
and map state are also available. Check every returned error.

The example registers timer `10` after the first purchase. Event timestamps and
timer timestamps use milliseconds. A timer callback sees its registering key,
can read restored state, and can emit records through a declared side output:

```go
tag := sdk.NewOutputTag("timer-results")
// Declare the tag on the result of ProcessWithTimers before attaching a branch.
stream.WithSideOutputs(tag)
stream.GetSideOutput(tag).AddSink(timerSink)
```

The complete example defines `stream` and `timerSink`. Undeclared tags fail the
invocation. Declared outputs without a connected branch are discarded.

For this bounded input, the final watermark fires the timer before the final
checkpoint snapshots the task. Unbounded inputs use their configured watermark
strategy; the default is five seconds of bounded out-of-orderness, emitted every
200ms. Configure `SetWatermarkStrategy` when those defaults do not match the
source. `GenerateWatermark` remains in the source interface for compatibility;
the runtime uses the configured strategy instead.

Managed Process state changes commit atomically only after a successful record
invocation. Record retries therefore do not retain failed state changes. This
does not undo external effects from user functions. `WithTTL` provides
processing-time expiry refreshed on writes, preserved across snapshots; it is
separate from event-time window retention.

## Configure checkpointing and recovery

The example configures:

```go
env.SetCheckpointInterval(100 * time.Millisecond).
    SetCheckpointTimeout(10 * time.Second).
    SetMinPauseBetweenCheckpoints(20 * time.Millisecond).
    SetRestartStrategy(sdk.FixedDelay(2, 10 * time.Millisecond)).
    SetStateBackend(sdk.NewHashMapStateBackend(8))
```

The hashmap limit is in MiB of logical state payload. Use
`sdk.NewPebbleStateBackend(directory)` for disk-backed state. In local worker
execution, explicit directories are isolated by job/operator/instance and
retained; temporary directories are cleaned up. In remote mode, the same selection is sent to managed Process/window operators.
`DataDir` then refers to a worker-local root, not a path on the submitting client.
Workers add job/operator/instance isolation. Custom registered operators must
accept backend factory configuration; unsupported operators fail deployment
instead of silently ignoring it. Upgrade workers and the coordinator together
before submitting graphs with this field.

The current checkpoint protocol allows one in-flight checkpoint per job. Savepoints and final checkpoints are
exempt from minimum pause.

`NoRestart()` is the SDK default. `FixedDelay` applies the same delay before each
retry, including the first. `ExponentialBackoff` grows it up to a configured cap.
`MaxAttempts` counts recovery deployments over the job lifetime, excluding the
initial deployment and requested rescales. These settings are persisted in
cluster job metadata. Embedded jobs with checkpointing or restarts enabled also
select the local coordinator/worker driver.

The source implements `CheckpointedSource`: `Checkpoint` returns its next input
offset, and `RestoreOffset` validates it after `Open`, before resumed reads.
Recovery mode fails the first instance after a second checkpoint starts. Since
only one checkpoint is active at a time, the first has completed. The replacement
source resumes at offset one, and Process restores count one and the pending
timer before processing the second purchase.

The collected sinks are ordinary sinks. The example chooses a failure boundary
with no uncheckpointed output, so its output contains no duplicates. General
replay can duplicate ordinary sink output; exactly-once external delivery
requires replayable sources and the full transactional sink contract. MiniCluster
retains metadata only for the current execution, so this example demonstrates
task recovery, not recovery after the hosting process crashes.

## Windows, harnesses and remote execution

For windowed processing, use `KeyBy(...).Window(...)` with TumblingWindow,
SlidingWindow or SessionWindow, followed by Aggregate, Reduce or Apply. Configure
AllowedLateness and a late-output tag before attaching the late branch. See
[WIP-12](trds/WIP-12/README.md) for update and purge semantics.

`NewProcessHarness` runs the same managed-state/timer adapter without workers.
It supports Process, AdvanceWatermark, Snapshot, Restore, SideOutput and a
controllable processing-time clock for TTL tests. Use MiniCluster when the test
needs graph routing, parallelism or actual checkpoint replication and recovery.
Cancel the Execute context to stop a run, or call MiniCluster.Shutdown to cancel
and join all its active runs.

Remote mode uses `SetMode(sdk.Cluster)` and `SetCoordinator(url)`. Remote graphs
must use named operators registered in the worker binary; Go closures are not
serialized. See the [registered worker example](../examples/wire-worker-example/main.go)
and [submission example](../examples/print-uppercase-graph/main.go). Workers and
the coordinator must understand the same graph features. HTTPS certificate
verification follows Go's HTTP client trust configuration; HTTP URLs are
unencrypted and appropriate only for a trusted local setup.

The [YAML format](../sdk/pipeline_yaml.md) compiles into SDK graphs. Its remaining
execution and reload scope belongs to [WIP-19](trds/WIP-19/README.md); do not infer
that every Go SDK capability is already available through YAML.

Explicit deployed backend roots also isolate deployment attempts. A retry before
the first checkpoint starts with empty managed state; a checkpointed retry imports
its authorized snapshot into the new attempt. Retained roots can contain state
from older attempts and require operator-managed disk retention.

## Registering operators in an application worker

Applications outside the Wire module can use `sdk.NewWorkerRegistry` and
`sdk.RunWorker`; they do not need to import `internal/worker` or `internal/engine`.
The [registered worker example](../sdk/examples/registered-worker/main.go) includes
both worker registration and a matching named job submission. With a coordinator
running on RPC `localhost:4002` and HTTP `localhost:4001`, run these in separate
terminals:

```sh
go run ./sdk/examples/registered-worker -mode worker
go run ./sdk/examples/registered-worker -mode submit
```

The worker prints `HELLO` and `WORLD`. This simple example uses one worker and no
checkpoints. The earlier stateful example covers checkpoint recovery.

Register source, sink, Map, FlatMap, Filter, KeyBy, Process and window classes
before starting a worker. Each factory receives application-defined config bytes
and `WorkerTaskContext`, including job, operator, subtask and attempt identities.
Return a fresh, unopened connector or closure for each invocation. Named classes
must be installed on every worker that can receive the job; submission does not
ship Go code. Duplicate registrations and nil factories panic immediately.

Process factories return `ProcessDefinition`; window factories return
`WindowDefinition` with exactly one of Aggregator, Reduce or Apply. Window dimensions
and lateness come from the submitted `Window(...).ApplyNamed(...)` graph. An
explicit graph state backend overrides the factory's backend default. Persistent
backend paths are scoped to the job, operator, instance and attempt.

`RunWorker` joins task shutdown before returning, within `ShutdownTimeout`
(default 30 seconds). Its context controls worker lifetime. For checkpoints,
configure distinct retained `CheckpointDirectory` roots on at least two workers.
`RPCTLSConfig` secures coordinator RPC only; this API does not imply HTTP,
data-plane or replica TLS. Those broader security requirements remain in WIP-17.
