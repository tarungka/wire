# User API & Go SDK

> **Feature/Project:** `User API & Go SDK`
>
> **WIP ID:** `WIP-14`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-22`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Wire's current documentation describes the engine's internal architecture (Chandy-Lamport checkpointing, Pebble state backend, Yamux transport) but provides **zero documentation on how a user actually writes, configures, or submits a Wire job**. There is no DataStream API, no Pipeline DSL, no Go SDK, no YAML schema — nothing that tells a developer "here's how you use Wire." Without this, Wire is an engine nobody can drive.

### 1.2 Proposed Solution (Technical Summary)

Define a Go SDK (`github.com/tarungka/wire/sdk`) that provides a fluent DataStream API for building stream processing pipelines. The SDK exposes: `StreamExecutionEnvironment` (entry point), `DataStream` (transformation chain), `KeyedStream` (keyed operations), `WindowedStream` (windowed aggregations), and a `Source`/`Sink` plugin model. For users who don't need custom Go code, Wire also supports declarative YAML pipeline definitions that map to the same internal graph representation.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define the Go SDK package structure and core types | SQL query interface |
| Document the DataStream API (Map, FlatMap, Filter, KeyBy, Window, Process) | Python/Java/Rust SDK bindings |
| Define YAML pipeline schema for declarative jobs | Visual pipeline builder / UI |
| Document state access API (ValueState, ListState, MapState) | Dynamic pipeline modification at runtime |
| Define watermark assignment and event time extraction | GPU acceleration API (deferred to future TRD) |
| Provide test harness and MiniCluster for development | Production deployment tooling |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| User can write a job from docs alone | Impossible (no docs) | Possible | Manual walkthrough test |
| Time to first working pipeline | Undefined | < 15 minutes | Developer trial |
| SDK API surface completeness | 0% | 100% of core operators | API review checklist |

---

## 2. Architecture & System Design

### 2.1 High-Level Architecture

```
User Code (Go SDK or YAML)
        │
        ▼
┌─────────────────────────┐
│ StreamExecutionEnvironment │  ← Entry point
│  ├── AddSource()         │
│  ├── SetParallelism()    │
│  ├── SetCheckpointInterval() │
│  └── Execute()           │
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────┐
│     StreamGraph          │  ← Logical DAG
│  (Nodes = Operators)     │
│  (Edges = Data Streams)  │
└────────────┬────────────┘
             │ Optimizer
             ▼
┌─────────────────────────┐
│     JobGraph             │  ← Optimized DAG (chaining, partitioning)
└────────────┬────────────┘
             │ Scheduler
             ▼
┌─────────────────────────┐
│   ExecutionGraph         │  ← Physical parallel instances
│  (distributed across     │
│   worker Task Slots)     │
└─────────────────────────┘
```

### 2.2 Component Breakdown

**Component 1:** `sdk.StreamExecutionEnvironment`
* **Responsibility:** Entry point for defining and executing Wire jobs. Holds global configuration (parallelism, checkpointing, restart strategy).
* **Technology:** Go struct in `sdk/` package
* **Interactions:** Builds a `StreamGraph`, submits to Coordinator via RPC or runs locally in embedded mode.

**Component 2:** `sdk.DataStream`
* **Responsibility:** Represents an unbounded sequence of events. Provides transformation operators (Map, FlatMap, Filter, KeyBy).
* **Technology:** Go generic struct `DataStream[T]` in `sdk/` package
* **Interactions:** Each method call appends a node to the StreamGraph. Terminal operations (AddSink) finalize the graph.

**Component 3:** `sdk.KeyedStream` / `sdk.WindowedStream`
* **Responsibility:** Keyed and windowed variants of DataStream for stateful and temporal operations.
* **Technology:** Go structs wrapping DataStream with key/window context
* **Interactions:** KeyedStream enables stateful Process functions and windowing. WindowedStream enables aggregation/reduction.

**Component 4:** `sdk.ProcessContext`
* **Responsibility:** Runtime context passed to user-defined Process functions. Provides state access, timers, and side output emission.
* **Technology:** Go interface in `sdk/` package
* **Interactions:** Backed by the Pebble state backend at runtime. Keyed state scoped to `(Key, OperatorID)`.

### 2.3 Data Flow

1. User creates `StreamExecutionEnvironment` and configures global settings.
2. User calls `env.AddSource()` to create the root `DataStream`.
3. User chains transformations: `.Map()`, `.Filter()`, `.KeyBy()`, `.Window()`, `.Aggregate()`.
4. User calls `.AddSink()` to terminate the pipeline.
5. User calls `env.Execute("job-name")` which:
   a. Builds the logical `StreamGraph`.
   b. Optimizes into `JobGraph` (operator chaining, shuffle insertion).
   c. Submits to Coordinator (cluster mode) or runs locally (embedded mode).

---

## 3. API Design

### 3.1 StreamExecutionEnvironment

```go
package sdk

type StreamExecutionEnvironment struct { ... }

func NewStreamExecutionEnvironment() *StreamExecutionEnvironment

// Configuration
func (env *StreamExecutionEnvironment) SetParallelism(n int)
func (env *StreamExecutionEnvironment) SetCheckpointInterval(d time.Duration)
func (env *StreamExecutionEnvironment) SetCheckpointTimeout(d time.Duration)
func (env *StreamExecutionEnvironment) SetMaxConcurrentCheckpoints(n int)
func (env *StreamExecutionEnvironment) SetMinPauseBetweenCheckpoints(d time.Duration)
func (env *StreamExecutionEnvironment) SetRestartStrategy(s RestartStrategy)
func (env *StreamExecutionEnvironment) SetStateBackend(b StateBackend)
func (env *StreamExecutionEnvironment) SetMode(m ExecutionMode) // Cluster | Embedded

// Pipeline construction
func (env *StreamExecutionEnvironment) AddSource(name string, source Source) *DataStream

// Execution
func (env *StreamExecutionEnvironment) Execute(jobName string) (*JobResult, error)
```

### 3.2 DataStream API

```go
// Stateless transformations
func (ds *DataStream) Map(name string, fn MapFunc) *DataStream
func (ds *DataStream) FlatMap(name string, fn FlatMapFunc) *DataStream
func (ds *DataStream) Filter(name string, fn FilterFunc) *DataStream

// Keying
func (ds *DataStream) KeyBy(name string, fn KeySelector) *KeyedStream

// Multi-stream
func (ds *DataStream) Union(other *DataStream) *DataStream
func (ds *DataStream) Connect(other *DataStream) *ConnectedStream

// Timestamps
func (ds *DataStream) AssignTimestamps(name string, fn TimestampExtractor) *DataStream

// Side outputs
func (ds *DataStream) GetSideOutput(tag OutputTag) *DataStream

// Sink
func (ds *DataStream) AddSink(name string, sink Sink)

// Function types
type MapFunc func(Event) (Event, error)
type FlatMapFunc func(Event) ([]Event, error)
type FilterFunc func(Event) (bool, error)
type KeySelector func(Event) ([]byte, error)
type TimestampExtractor func(Event) int64
```

### 3.3 KeyedStream & WindowedStream API

```go
// Stateful processing on keyed stream
func (ks *KeyedStream) Process(name string, fn ProcessFunc) *DataStream
func (ks *KeyedStream) Window(w WindowAssigner) *WindowedStream

// Window assignments
func TumblingWindow(size time.Duration) WindowAssigner
func SlidingWindow(size, slide time.Duration) WindowAssigner
func SessionWindow(gap time.Duration) WindowAssigner

// Window operations
func (ws *WindowedStream) Aggregate(name string, agg Aggregator) *DataStream
func (ws *WindowedStream) Reduce(name string, fn ReduceFunc) *DataStream
func (ws *WindowedStream) Apply(name string, fn WindowFunc) *DataStream
func (ws *WindowedStream) AllowedLateness(d time.Duration) *WindowedStream

// Built-in aggregators
func CountAggregator() Aggregator
func SumAggregator(field string) Aggregator
func MinAggregator(field string) Aggregator
func MaxAggregator(field string) Aggregator
func AvgAggregator(field string) Aggregator

// Process function with state access
type ProcessFunc func(ctx ProcessContext, event Event) ([]Event, error)

type ProcessContext interface {
    GetState(name string) ValueState
    GetListState(name string) ListState
    GetMapState(name string) MapState
    EmitToSideOutput(tag OutputTag, event Event)
    CurrentKey() []byte
    CurrentEventTime() int64
    CurrentWatermark() int64
    RegisterEventTimeTimer(timestamp int64)
}
```

### 3.4 State API

```go
type ValueState interface {
    Value() ([]byte, error)
    ValueInt64() (int64, error)
    ValueFloat64() (float64, error)
    ValueString() (string, error)
    Set(value []byte) error
    SetInt64(value int64) error
    SetFloat64(value float64) error
    SetString(value string) error
    Clear() error
    WithTTL(ttl time.Duration) ValueState
}

type ListState interface {
    Get() ([][]byte, error)
    Add(value []byte) error
    Clear() error
    WithTTL(ttl time.Duration) ListState
}

type MapState interface {
    Get(key []byte) ([]byte, error)
    Put(key []byte, value []byte) error
    Remove(key []byte) error
    Keys() ([][]byte, error)
    Entries() (map[string][]byte, error)
    Clear() error
    WithTTL(ttl time.Duration) MapState
}
```

### 3.5 YAML Pipeline Schema

For users who prefer declarative configuration over Go code:

```yaml
name: "job-name"                     # Required: unique job identifier
parallelism: 4                       # Default parallelism for all operators

checkpoint:
  interval: "10s"
  timeout: "10m"
  min_pause: "0s"

restart:
  strategy: "fixed-delay"            # fixed-delay | exponential-backoff | none
  attempts: 3
  delay: "10s"

sources:
  - name: "source-name"
    type: "http-api"                 # Connector type (see WIP-16)
    parallelism: 8
    watermark:
      strategy: "bounded-ooo"       # bounded-ooo | monotonic | ingestion-time
      max_ooo: "5s"
    config:
      url: "https://upstream.example.com/events"
      poll_interval: "1s"

transforms:
  - name: "parse-json"
    type: "json-parse"
    input: "source-name"
  - name: "filter-valid"
    type: "filter"
    input: "parse-json"
    config:
      expression: "status == 'active'"
  - name: "key-by-user"
    type: "key-by"
    input: "filter-valid"
    config:
      key_field: "user_id"
  - name: "count-window"
    type: "tumbling-window"
    input: "key-by-user"
    config:
      size: "5m"
      aggregation: "count"
      allowed_lateness: "30s"

sinks:
  - name: "output"
    type: "http-api"
    input: "count-window"
    config:
      url: "https://downstream.example.com/results"
      method: "POST"
```

**Built-in Transform Types:**

| Type | Description | Config Fields |
|------|-------------|---------------|
| `json-parse` | Parse JSON bytes to structured event | `timestamp_field` |
| `filter` | Drop events not matching expression | `expression` |
| `key-by` | Partition stream by field | `key_field` |
| `select` | Project specific fields | `fields` |
| `rename` | Rename fields | `mapping` |
| `tumbling-window` | Fixed non-overlapping window | `size`, `aggregation`, `allowed_lateness` |
| `sliding-window` | Overlapping window | `size`, `slide`, `aggregation`, `allowed_lateness` |
| `session-window` | Gap-based window | `gap`, `aggregation`, `allowed_lateness` |

---

## 4. Data Model & Storage

### 4.1 Core Types

```go
// Event is the atomic unit of data flowing through Wire.
type Event struct {
    Key       []byte            // Partition key (optional, may be nil)
    Value     []byte            // Payload
    EventTime int64             // Unix milliseconds
    Headers   map[string][]byte // Optional metadata
}

// OutputTag identifies a side output channel.
type OutputTag struct {
    ID string
}

func NewOutputTag(id string) OutputTag

// RestartStrategy defines failure recovery behavior.
type RestartStrategy struct {
    Type           string        // "fixed-delay" | "exponential-backoff" | "none"
    MaxAttempts    int
    Delay          time.Duration
    InitialDelay   time.Duration
    MaxDelay       time.Duration
    Multiplier     float64
}

func FixedDelay(attempts int, delay time.Duration) RestartStrategy
func ExponentialBackoff(attempts int, initial, max time.Duration, multiplier float64) RestartStrategy
func NoRestart() RestartStrategy

// ExecutionMode determines how the job runs.
type ExecutionMode int
const (
    Cluster  ExecutionMode = iota // Submit to remote coordinator
    Embedded                       // Run in-process (dev/test)
)
```

### 4.2 Storage Considerations

* **State storage:** All keyed state is stored in Pebble, scoped to `(KeyGroup, OperatorID, UserKey)`. See WIP-03 for key encoding.
* **Checkpoint storage:** State snapshots are uploaded to S3/MinIO or local filesystem. See `state-backend.md` Canon doc.
* **Job metadata:** Job graph, configuration, and checkpoint history stored in Coordinator's metadata store (see WIP-09).

---

## 5. Design Decisions & Trade-offs

### Decision 1: Go SDK (not YAML-only)

|  |  |
| -- | -- |
| **Context** | Users need to express arbitrary computation (custom parsing, stateful logic, ML inference), not just static DAGs. |
| **Options Considered** | (A) Go SDK only, (B) YAML-only declarative, (C) Both Go SDK + YAML |
| **Decision** | Option C: Both Go SDK and YAML |
| **Rationale** | Go SDK handles complex use cases; YAML handles simple ETL pipelines without compilation. Same internal graph representation. |
| **Trade-offs Accepted** | Two codepaths to maintain (SDK graph builder + YAML parser). YAML is less expressive. |
| **Revisit Trigger** | If YAML adoption is < 10% after 6 months, consider dropping it. |

### Decision 2: Fluent API (method chaining)

|  |  |
| -- | -- |
| **Context** | API ergonomics matter for adoption. |
| **Options Considered** | (A) Fluent/chaining API (`stream.Map().Filter().Sink()`), (B) Builder pattern with explicit graph construction, (C) Functional composition |
| **Decision** | Option A: Fluent API |
| **Rationale** | Matches Flink, Spark Structured Streaming, and other stream processing SDKs. Familiar to target audience. Pipeline reads left-to-right. |
| **Trade-offs Accepted** | Error handling deferred to `Execute()` rather than per-operator. Type safety limited by Go's generics. |
| **Revisit Trigger** | If Go generics mature enough to support proper typed streams, refactor to use them. |

### Decision 3: `[]byte` for Event Key/Value (not generics)

|  |  |
| -- | -- |
| **Context** | Need to decide on event payload representation. |
| **Options Considered** | (A) `[]byte` with manual serialization, (B) `interface{}` with reflection, (C) Go generics `DataStream[T]` |
| **Decision** | Option A: `[]byte` |
| **Rationale** | Zero-copy through the pipeline. No serialization overhead between operators in the same chain. User controls serialization format. Matches common message broker semantics. |
| **Trade-offs Accepted** | User must manually serialize/deserialize in Map/Process functions. Less type safety. |
| **Revisit Trigger** | If Go generics support improves and user demand for typed streams is high. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | User Map function panics | Runtime catches panic via `recover()`, wraps as error, routes event to DLQ (see WIP-11) | Single event lost | Medium |
| 2 | User Map function returns error | Event routed to DLQ side output if configured, otherwise job fails | Depends on DLQ config | Medium |
| 3 | YAML references non-existent transform type | Fail at validation before job submission, return 400 | No job started | Low |
| 4 | Circular dependency in YAML transforms | Detected during graph validation, rejected with descriptive error | No job started | Low |
| 5 | State TTL expires mid-window | Window aggregation sees zero/default state. Documented as expected behavior. | Potential data loss | Medium |

---

## 7. Security & Compliance

### 7.1 Authentication & Authorization

* SDK communicates with Coordinator via authenticated RPC (see WIP-17 for auth model).
* Job submission requires valid credentials.
* YAML configs support `${ENV_VAR}` substitution for secrets — secrets never stored in pipeline config.

### 7.2 Data Protection

* **In transit:** All data between SDK client and Coordinator encrypted via TLS.
* **At rest:** State in Pebble inherits worker disk encryption. Checkpoints in S3 use server-side encryption.

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | SDK graph builder, operator logic, YAML parser | Go `testing` package | >= 80% |
| Integration Tests | End-to-end pipeline with MiniCluster | `sdk.MiniCluster` | All operator types |
| Example Tests | Documented examples compile and run | Go test files in `sdk/examples/` | All examples |

### 8.1 Key Test Scenarios

1. Map → Filter → Sink pipeline produces correct output
2. KeyBy → Window → Aggregate produces correct windowed counts
3. State persistence survives checkpoint/restore cycle
4. YAML pipeline parses and produces identical graph to equivalent Go SDK code
5. Invalid YAML rejected with descriptive error messages
6. Side output routing works for late data and DLQ
7. MiniCluster handles parallelism > 1 correctly

### 8.2 Test Harness

```go
// sdk.TestHarness for unit-testing individual operators
harness := sdk.NewTestHarness()
harness.AddInput(sdk.Event{Key: []byte("k"), Value: []byte("v"), EventTime: now})
output := harness.RunMap(myMapFunc)
assert.Len(t, output, 1)

// sdk.MiniCluster for integration testing full pipelines
cluster := sdk.NewMiniCluster(sdk.MiniClusterConfig{NumTaskSlots: 4})
defer cluster.Shutdown()
env := cluster.GetExecutionEnvironment()
// ... build pipeline ...
result, err := env.Execute("test-job")
```

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should we support generics-based typed streams (`DataStream[T]`) from day one, or start with `[]byte` and add typed wrappers later? | Tarun | Open |
| 2 | How does YAML pipeline hot-reload work? Full restart or incremental graph update? | Tarun | Open |
| 3 | Should the test harness support event-time timers and watermark advancement? | Tarun | Open |
| 4 | What expression language should YAML filter use? CEL? A custom DSL? Go templates? | Tarun | Open |
| 5 | Risk: Go generics limitations may force awkward API ergonomics for typed state access | — | Acknowledged |
