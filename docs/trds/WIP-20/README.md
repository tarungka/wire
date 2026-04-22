# Task Execution Engine

> **Feature/Project:** `Task Execution Engine`
>
> **WIP ID:** `WIP-20`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-03-06`
>
> **Last Updated:** `2026-03-06`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-03-06 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Workers receive `DeployTask` commands and report `TaskStatusRunning`, but **no actual data processing happens**. The `handleDeployTask` in `internal/worker/worker.go` decodes the `TaskDescriptor`, creates a cancel context, and immediately reports RUNNING --- it is a control-plane stub with no data plane.

The engine package (`internal/engine/`) has a fully functional `TaskSlot` that orchestrates source readers, operator chains, output writers, watermarks, and checkpoints. The SDK (`sdk/`) has a working embedded executor that builds operator chains from `StreamNode` graphs using adapters (`mapAdapter`, `sinkAdapter`, etc.).

**The missing piece:** there is no mechanism for user-defined operator logic (Go functions, sources, sinks) to reach the worker process at runtime. In Go, functions are not serializable --- they cannot be sent over the network or stored in a job graph. This is a fundamental architectural gap: the `OperatorDescriptor.ClassName` field exists in the RPC messages but nothing resolves it to a live `engine.Operator` instance.

### 1.2 Proposed Solution (Technical Summary)

Introduce an **operator registry** pattern where named factory functions are registered at compile time and looked up by name at deploy time. When a worker receives a `DeployTask` command, the new **task executor** resolves operator names from the `JobGraph` via the registry, instantiates the operators, wires them into an `engine.TaskSlot`, and calls `TaskSlot.Run()`.

This follows the same model used by `database/sql` (driver registration), `image` (format registration), and `encoding` (codec registration) in the Go standard library. User code and the worker binary are compiled together into a single binary --- the same approach as Apache Flink's fat-jar, adapted to Go's static compilation model.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define operator registry API for sources, operators, and sinks | Hot code deployment / dynamic loading |
| Bridge `JobGraph` operator descriptors to live `engine.Operator` instances | WASM/scripting runtime for user functions |
| Build task executor that constructs and runs `engine.TaskSlot` | Multi-operator graphs with network shuffle between tasks |
| Wire executor into existing `handleDeployTask` lifecycle | Automatic operator fusion optimization |
| Report correct task status transitions (RUNNING, FAILED, FINISHED) | State backend integration for operator checkpoints |
| Include one built-in test pipeline to prove end-to-end execution | Production-grade connectors (Kafka, etc.) |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Worker executes user logic | No (stub only) | Yes | Integration test: source emits events, sink receives them |
| End-to-end job lifecycle | Stops at RUNNING (no processing) | RUNNING with processing, FINISHED on source exhaustion | Job status transitions in coordinator |
| Operator lookup errors reported | Silent failure | TaskStatusFailed with error message | Worker logs + coordinator task status |

---

## 2. Architecture & System Design

### 2.1 Operator Registry

A global, process-level registry maps string names to factory functions. Registration happens at `init()` time, before the worker starts.

```go
// internal/worker/registry.go

// SourceFactory creates a SourceOperator from serialized config.
type SourceFactory func(config []byte) (engine.SourceOperator, error)

// OperatorFactory creates an Operator from serialized config.
type OperatorFactory func(config []byte) (engine.Operator, error)

// SinkFactory creates a SinkOperator from serialized config.
type SinkFactory func(config []byte) (engine.SinkOperator, error)

// RegisterSource registers a named source factory.
func RegisterSource(name string, factory SourceFactory)

// RegisterOperator registers a named operator factory.
func RegisterOperator(name string, factory OperatorFactory)

// RegisterSink registers a named sink factory.
func RegisterSink(name string, factory SinkFactory)

// LookupSource returns the source factory for the given name, or an error.
func LookupSource(name string) (SourceFactory, error)

// LookupOperator returns the operator factory for the given name, or an error.
func LookupOperator(name string) (OperatorFactory, error)

// LookupSink returns the sink factory for the given name, or an error.
func LookupSink(name string) (SinkFactory, error)
```

**Registration pattern** (in user code or built-in connectors):

```go
package myconnectors

import "github.com/tarungka/wire/internal/worker"

func init() {
    worker.RegisterSource("my-source", func(config []byte) (engine.SourceOperator, error) {
        var cfg MySourceConfig
        if err := json.Unmarshal(config, &cfg); err != nil {
            return nil, err
        }
        return NewMySource(cfg), nil
    })
}
```

The registry is a simple `sync.RWMutex`-protected `map[string]Factory`. Registration after the worker starts is a programming error and panics (same as `database/sql.Register`).

### 2.2 Job Config to Worker Data Flow

Currently `TaskDescriptor` carries only task-level metadata (task ID, operator ID, key group range, upstream/downstream channels). The operator's `ClassName` and `Config` bytes live in the `JobGraph`'s `OperatorDescriptor`, but the `JobGraph` is not sent to the worker as part of `DeployTask`.

**Data flow for MVP:**

```
User Code          SDK                  Coordinator              Worker
   |                |                       |                      |
   |-- StreamGraph->|                       |                      |
   |                |-- toJobGraph() ------>|                      |
   |                |   (sets ClassName,    |                      |
   |                |    Config on each     |                      |
   |                |    OperatorDescriptor) |                      |
   |                |                       |-- Store JobGraph --->|
   |                |                       |   via JobGraphKey()  |
   |                |                       |                      |
   |                |                       |== DeployTask cmd ===>|
   |                |                       |   (TaskDescriptor    |
   |                |                       |    + JobGraph in     |
   |                |                       |    command Data)     |
   |                |                       |                      |
   |                |                       |                      |-- Decode TaskDescriptor
   |                |                       |                      |-- Decode JobGraph
   |                |                       |                      |-- Lookup operators by ClassName
   |                |                       |                      |-- Build TaskSlot
   |                |                       |                      |-- TaskSlot.Run()
```

**Option A (recommended for MVP): Embed JobGraph in DeployTask command.**

The `DeployTask` command's `Data` field currently carries only the `TaskDescriptor`. Extend this to carry a `DeployTaskPayload` containing both:

```go
// DeployTaskPayload carries everything the worker needs to run a task.
type DeployTaskPayload struct {
    Task  TaskDescriptor `codec:"t"`
    Graph JobGraph       `codec:"g"`
}
```

This is simple and avoids a new RPC. The `JobGraph` is small (operator descriptors + edges) and is identical for all tasks in a job, so there is some redundancy when deploying multiple tasks to the same worker. This is acceptable for MVP.

**Option B (future): FetchJobGraph RPC.**

Add a `FetchJobGraph` RPC that the worker calls to retrieve the graph by job ID. This avoids sending the graph with every task but adds a round trip and a new RPC method. Deferred to a future WIP.

### 2.3 SDK Changes: Populating ClassName and Config

The SDK's `graph_converter.go` currently produces `OperatorDescriptor` entries without setting `ClassName` or `Config`. To bridge user-defined functions to the registry:

1. `StreamNode` gains a `ClassName string` field, set by the user when building the graph (e.g., `stream.Map("my-mapper", myMapFn)`).
2. `StreamNode` gains a `Config []byte` field for operator-specific configuration.
3. `toJobGraph()` propagates these fields to `OperatorDescriptor.ClassName` and `OperatorDescriptor.Config`.

For the embedded executor, `ClassName` is ignored --- the function pointer is used directly (as today). For cluster mode, the function pointer is not serialized; instead the `ClassName` is used to look up the factory in the registry.

### 2.4 Task Executor

New file: `internal/worker/executor.go`

The task executor is the bridge between the control plane (`handleDeployTask`) and the data plane (`engine.TaskSlot`).

```go
// executor.go

// TaskExecutor runs a single task by resolving operators from the registry
// and delegating to engine.TaskSlot.
type TaskExecutor struct {
    taskDesc rpc.TaskDescriptor
    graph    rpc.JobGraph
    log      zerolog.Logger
}

// Run resolves operators, builds a TaskSlot, and runs it.
// Returns nil on clean completion (source exhausted), or an error on failure.
func (e *TaskExecutor) Run(ctx context.Context) error {
    // 1. Find this task's operator in the graph.
    opDesc := e.findOperator(e.taskDesc.OperatorID)

    // 2. Resolve operator from registry based on Type and ClassName.
    source, operators, err := e.resolveOperators(opDesc)

    // 3. Build TaskSlot.
    cfg := engine.DefaultTaskSlotConfig()
    slot := engine.NewTaskSlot(cfg, nil, nil, operators, source)
    slot.TaskID = e.taskDesc.TaskID
    slot.TaskIndex = int(e.taskDesc.SubtaskIndex)

    // 4. Open all operators.
    if err := e.openOperators(ctx, source, operators); err != nil {
        return fmt.Errorf("executor: open operators: %w", err)
    }
    defer e.closeOperators(source, operators)

    // 5. Run the TaskSlot.
    return slot.Run(ctx)
}
```

**Operator resolution logic:**

| `OperatorDescriptor.Type` | Registry Lookup | Result |
|---------------------------|----------------|--------|
| `OperatorTypeSource` | `LookupSource(className)` | Sets `TaskSlot.Source` |
| `OperatorTypeMap`, `OperatorTypeFlatMap`, `OperatorTypeFilter`, `OperatorTypeProcess` | `LookupOperator(className)` | Appended to `TaskSlot.Operators` |
| `OperatorTypeSink` | `LookupSink(className)` | Wrapped as last entry in `TaskSlot.Operators` |

For MVP, the executor handles **single-operator-per-task** graphs (one source task, one sink task, or a fused chain). Multi-operator graphs with network shuffle between tasks are deferred.

### 2.5 Lifecycle Integration

The existing `handleDeployTask` in `worker.go` is updated to use the executor:

```go
func (w *Worker) handleDeployTask(cmd rpc.WorkerCommand) {
    // ... existing idempotency check ...

    // Decode payload (now DeployTaskPayload instead of bare TaskDescriptor).
    var payload rpc.DeployTaskPayload
    if err := protocol.DecodeMsgPack(cmd.Data, &payload); err != nil {
        w.reportTaskFailed(cmd.JobID, cmd.TaskID, "decode error: "+err.Error())
        return
    }

    taskCtx, cancel := context.WithCancel(context.Background())
    w.mu.Lock()
    w.tasks[cmd.TaskID] = cancel
    w.mu.Unlock()

    go func() {
        defer cancel()
        defer w.removeTask(cmd.TaskID)

        executor := &TaskExecutor{
            taskDesc: payload.Task,
            graph:    payload.Graph,
            log:      w.log.With().Str("task_id", cmd.TaskID).Logger(),
        }

        // Report RUNNING after successful operator open.
        w.reportTaskStatus(cmd.JobID, cmd.TaskID, rpc.TaskStatusRunning)

        err := executor.Run(taskCtx)
        if err != nil {
            w.reportTaskStatus(cmd.JobID, cmd.TaskID, rpc.TaskStatusFailed)
        } else {
            w.reportTaskStatus(cmd.JobID, cmd.TaskID, rpc.TaskStatusFinished)
        }
    }()
}
```

**Status transition mapping:**

| Event | Task Status Reported | Coordinator Reaction |
|-------|---------------------|---------------------|
| Operators opened successfully | `TaskStatusRunning` | Job may transition to RUNNING (when all tasks report RUNNING) |
| `TaskSlot.Run()` returns `nil` (source exhausted) | `TaskStatusFinished` | Job transitions to FINISHING |
| `TaskSlot.Run()` returns error | `TaskStatusFailed` | Job transitions to FAILING, restart strategy evaluated |
| Context canceled (CancelTask command) | `TaskStatusCanceled` | Job transitions to CANCELING/CANCELED |
| Panic recovered | `TaskStatusFailed` with stack trace | Same as error |

### 2.6 Panic Recovery

The executor goroutine wraps `executor.Run()` with a deferred panic recovery:

```go
defer func() {
    if r := recover(); r != nil {
        stack := string(debug.Stack())
        w.reportTaskFailed(cmd.JobID, cmd.TaskID, fmt.Sprintf("panic: %v\n%s", r, stack))
    }
}()
```

This prevents a user operator panic from crashing the entire worker process.

---

## 3. Design Decisions & Trade-offs

### Decision 1: Registry pattern over plugin/reflection

|  |  |
| -- | -- |
| **Context** | Go functions cannot be serialized over the network. Workers need a way to resolve operator names to live instances. |
| **Options Considered** | (A) Global registry with named factories, (B) Go plugin system (`plugin.Open`), (C) Reflection-based instantiation (`reflect.New`) |
| **Decision** | Option A: Global registry |
| **Rationale** | Go plugins are fragile (must be compiled with identical Go version and dependency set), poorly supported on some platforms, and add deployment complexity. Reflection cannot instantiate arbitrary function types. The registry pattern is idiomatic Go (`database/sql`, `image`, `encoding`) and provides compile-time type safety. |
| **Trade-offs Accepted** | User code must be compiled into the worker binary. No hot deploy for v1. |
| **Revisit Trigger** | If users demand dynamic code loading without recompilation. Consider WASM at that point. |

### Decision 2: Single-binary compilation model

|  |  |
| -- | -- |
| **Context** | The worker needs access to user-defined operator logic at runtime. |
| **Options Considered** | (A) Single binary (user code + worker compiled together), (B) Separate binaries with IPC, (C) WASM sandbox |
| **Decision** | Option A: Single binary |
| **Rationale** | Same model as Flink's fat-jar but with Go's static compilation. Simple deployment (one binary), full Go performance, access to all Go libraries. IPC adds latency and serialization overhead. WASM has poor Go interop and significant performance overhead. |
| **Trade-offs Accepted** | Recompilation required for code changes. Worker binary includes all user dependencies. |
| **Revisit Trigger** | If multi-tenant deployment requires isolation between user jobs. |

### Decision 3: Embed JobGraph in DeployTask (not a separate RPC)

|  |  |
| -- | -- |
| **Context** | Workers need the `JobGraph` (operator names, types, configs) to resolve operators. |
| **Options Considered** | (A) Embed in DeployTask command payload, (B) New `FetchJobGraph` RPC |
| **Decision** | Option A: Embed in payload |
| **Rationale** | Simpler (no new RPC), atomic (task + graph arrive together), no failure mode from a missing graph fetch. The `JobGraph` is small (typically <1KB). Redundancy across tasks on the same worker is negligible. |
| **Trade-offs Accepted** | Same graph bytes sent per task (N copies for N tasks in a job). |
| **Revisit Trigger** | If job graphs grow large (e.g., hundreds of operators with large configs). |

---

## 4. Dependencies

| WIP | Dependency | What This WIP Uses |
|-----|-----------|-------------------|
| WIP-02 | Goroutine & Concurrency Model | `TaskSlot.Run()` goroutine patterns, `errgroup` usage |
| WIP-07 | RPC Interface | `UpdateTaskStatusRequest`, `TaskDescriptor`, `WorkerCommand`, `DeployTask` command type |
| WIP-14 | User API & Go SDK | `StreamGraph`, adapters (`mapAdapter`, `sinkAdapter`, etc.), `graph_converter.go` |
| WIP-15 | Job Lifecycle | Scheduler, `DeployTask` command flow, job status transitions (DEPLOYING, RUNNING, FINISHING, FAILING) |
| WIP-16 | Connector SDK | Source/Sink interfaces for built-in connectors registered via the operator registry |

---

## 5. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Worker receives DeployTask for unknown operator `ClassName` | Registry lookup returns error. Worker reports `TaskStatusFailed` with message "unknown operator: {name}". | Task fails, job enters FAILING. | Medium |
| 2 | Operator factory returns error (bad config) | Worker reports `TaskStatusFailed` with factory error message. | Task fails, job enters FAILING. | Medium |
| 3 | Operator `Open()` returns error | Worker reports `TaskStatusFailed`. Already-opened operators are closed. | Task fails, job enters FAILING. | Medium |
| 4 | Task panics during `TaskSlot.Run()` | Panic is recovered. Worker reports `TaskStatusFailed` with panic message and stack trace. | Task fails, worker remains healthy. | High |
| 5 | Source returns nil batch (EOF) | `TaskSlot.Run()` returns nil. Worker reports `TaskStatusFinished`. | Normal completion for bounded sources. | Low |
| 6 | Duplicate DeployTask for same task ID | Idempotent: already handled in current code (`w.tasks[taskID]` check). | No effect. | Low |
| 7 | CancelTask received while executor is running | Context is canceled. `TaskSlot.Run()` returns `context.Canceled`. Worker reports `TaskStatusCanceled`. | Clean cancellation. | Low |
| 8 | Worker receives DeployTask but registry has no registrations | All operator lookups fail. Likely a deployment error (user forgot to register operators or imported the wrong binary). Error message guides user. | All tasks fail. | Medium |

---

## 6. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Registry register/lookup, duplicate registration panic, unknown name error | Go `testing` | 100% of registry operations |
| Unit Tests | Executor builds TaskSlot correctly from OperatorDescriptors | Go `testing` | All operator types (source, map, flatmap, filter, sink) |
| Unit Tests | Executor reports correct status on success, error, panic | Go `testing` | All status transitions |
| Integration Tests | Full pipeline: submit job, scheduler deploys, worker runs built-in test pipeline, job reaches RUNNING, source exhausts, FINISHED | Go `testing` + MiniCluster | End-to-end lifecycle |

### 6.1 Built-in Test Pipeline

A built-in test pipeline registered under well-known names for integration testing:

```go
func init() {
    // Counter source: emits N events then returns nil (EOF).
    RegisterSource("wire.test.counter-source", func(config []byte) (engine.SourceOperator, error) {
        // config: {"count": 100}
        return NewCounterSource(count), nil
    })

    // Passthrough operator: forwards events unchanged.
    RegisterOperator("wire.test.passthrough", func(config []byte) (engine.Operator, error) {
        return &passthroughOperator{}, nil
    })

    // Discard sink: accepts events and discards them.
    RegisterSink("wire.test.discard-sink", func(config []byte) (engine.SinkOperator, error) {
        return &discardSink{}, nil
    })
}
```

### 6.2 Key Test Scenarios

1. **Registry:** Register source, operator, sink; look up by name; verify unknown name returns error; verify duplicate registration panics.
2. **Executor unit:** Given a `JobGraph` with a counter source and discard sink, executor builds `TaskSlot` and runs to completion.
3. **Executor error:** Given a `JobGraph` with an unknown `ClassName`, executor returns error immediately.
4. **Executor panic:** Register an operator that panics; verify executor recovers and reports `TaskStatusFailed`.
5. **Integration:** Start coordinator + worker, submit job with test pipeline graph, verify job transitions: CREATED -> DEPLOYING -> RUNNING -> FINISHING -> FINISHED.

---

## 7. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should `FetchJobGraph` be a new RPC or should the full graph always be embedded in DeployTask? Embedding is simpler but redundant for multi-task jobs. | Tarun | Decided: embed for MVP, defer RPC to future WIP |
| 2 | Should the registry support operator versioning (e.g., `my-source@v2`)? | Tarun | Open |
| 3 | How should multi-operator graphs work? A single task currently runs a single fused chain. With shuffle boundaries, tasks on different workers need network transport. | Tarun | Open (deferred --- requires network data plane, separate WIP) |
| 4 | Should the executor report `TaskStatusRunning` before or after `Open()`? Reporting before is faster but may report RUNNING for a task that fails to open. | Tarun | Decided: report after successful Open |
| 5 | Risk: Large operator configs (e.g., ML model weights in `Config` bytes) could bloat the DeployTask payload. Mitigation: use a URI reference instead of inline bytes for large configs. | -- | Acknowledged |
| 6 | Risk: A panicking operator could leave resources (file handles, connections) unreleased. Mitigation: defer `Close()` calls before the panic recovery handler. | -- | Acknowledged |

---

## Appendix: Key Source Files

| File | What It Provides |
|------|-----------------|
| `internal/engine/operator.go` | `Operator`, `MapOperator`, `FlatMapOperator`, `SourceOperator`, `SinkOperator` interfaces |
| `internal/engine/task_slot.go` | `TaskSlot` struct and `Run()` --- the execution runtime |
| `internal/engine/config.go` | `TaskSlotConfig`, `DefaultTaskSlotConfig()` |
| `sdk/adapters.go` | `mapAdapter`, `filterAdapter`, `sourceAdapter`, `sinkAdapter` --- adapter pattern to reuse |
| `sdk/embedded.go` | `runLinearInstance()` --- reference for building operator chains from StreamNodes |
| `sdk/graph_converter.go` | `toJobGraph()` --- converts StreamGraph to `rpc.JobGraph` |
| `internal/rpc/messages.go` | `TaskDescriptor`, `OperatorDescriptor`, `JobGraph`, `UpdateTaskStatusRequest` |
| `internal/worker/worker.go` | Current `handleDeployTask()` stub to be replaced |
| `internal/coordinator/scheduler.go` | `generateTaskDescriptors()` --- currently generates dummy single-operator tasks |
