# Technical Requirements Document (TRD)

> **Feature/Project:** `Connector SDK & Built-in Connectors`
>
> **WIP ID:** `WIP-02`
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

Wire's architecture docs reference connectors (Kafka, SQS, Postgres, S3, Redis, Elasticsearch, etc.) as "built-in" but **zero connector code exists, no interfaces are defined, and no configuration is documented**. The AGENTS.md lists Source/Sink interfaces (`Connect()`, `Read()`, `Write()`, `Close()`) but these don't exist in the codebase and are too simplistic for Wire's exactly-once guarantees (they lack offset management, watermark generation, and transactional commit hooks).

### 1.2 Proposed Solution (Technical Summary)

Define a connector SDK with two core interfaces — `Source` (replayable reads with offset tracking and watermark generation) and `Sink` (batch writes with optional transactional semantics). Additionally define a `TransactionalSink` extension for exactly-once delivery via two-phase commit tied to Wire's checkpoint lifecycle. Provide built-in implementations for 11 connectors and a registration mechanism for custom connectors.

### 1.3 Goals & Non-Goals

| Goals (In Scope) | Non-Goals (Explicitly Out) |
| -- | -- |
| Define Source interface with replay, offset, and watermark support | CDC (Change Data Capture) framework |
| Define Sink interface with batch writes | Exactly-once for non-transactional sinks |
| Define TransactionalSink for 2PC exactly-once | Schema registry integration |
| Document config for 11 built-in connectors | Dynamic connector loading (plugins at runtime) |
| Provide connector registration/factory mechanism | Connector performance benchmarks |
| Document custom connector development guide | Connector auto-discovery |

### 1.4 Success Metrics

| Metric | Current Baseline | Target | Measurement |
| -- | -- | -- | -- |
| Defined connector interfaces | 0 | 2 (Source + Sink) + 1 (TransactionalSink) | Code review |
| Documented built-in connectors | 0 | 11 | Doc review |
| Custom connector can be written from docs | Impossible | < 1 hour | Developer trial |

---

## 2. Architecture & System Design

### 2.1 High-Level Architecture

```
┌──────────────────────────────────────────────────────┐
│                    Wire Runtime                       │
│                                                       │
│  ┌─────────┐    ┌──────────┐    ┌─────────────────┐ │
│  │ Source   │───▶│ Operator │───▶│ Sink            │ │
│  │ (reads)  │    │  Chain   │    │ (writes)        │ │
│  └────┬─────┘    └──────────┘    └───────┬─────────┘ │
│       │                                   │           │
│  RestoreOffset()                    WriteBatch()      │
│  ReadBatch()                        PreCommit()       │
│  GenerateWatermark()                Commit()          │
│       │                              Abort()          │
└───────┼──────────────────────────────┼────────────────┘
        │                              │
        ▼                              ▼
  ┌───────────┐                 ┌────────────┐
  │ External  │                 │  External  │
  │ System    │                 │  System    │
  │ (Kafka,   │                 │ (Postgres, │
  │  SQS...)  │                 │  S3, ES...) │
  └───────────┘                 └────────────┘
```

### 2.2 Component Breakdown

**Component 1:** `sdk.Source` interface
* **Responsibility:** Read events from an external system with replayability.
* **Technology:** Go interface in `sdk/` package
* **Interactions:** Called by Task Slot runtime. `RestoreOffset` called during recovery. `GenerateWatermark` called periodically.

**Component 2:** `sdk.Sink` interface
* **Responsibility:** Write events to an external system.
* **Technology:** Go interface in `sdk/` package
* **Interactions:** Called by Task Slot runtime. `WriteBatch` called with buffered events.

**Component 3:** `sdk.TransactionalSink` interface
* **Responsibility:** Extends Sink with transaction support for exactly-once. Participates in checkpoint-driven two-phase commit.
* **Technology:** Go interface extending `sdk.Sink`
* **Interactions:** `BeginTransaction` called at start, `PreCommit` on barrier receipt, `Commit` on global checkpoint completion, `Abort` on failure.

**Component 4:** Connector Registry
* **Responsibility:** Maps connector type strings (e.g., "kafka") to factory functions for Source/Sink creation.
* **Technology:** Global registry with `RegisterSource` / `RegisterSink` functions
* **Interactions:** YAML parser and SDK look up connectors by type string.

### 2.3 Data Flow

**Source data flow:**
1. Runtime calls `source.Open(ctx, config)` on task start.
2. Runtime calls `source.RestoreOffset(ctx, offset)` if recovering from checkpoint.
3. Runtime loops: `events, offset, err := source.ReadBatch(ctx, maxBatch)`
4. Runtime periodically calls `source.GenerateWatermark()` to advance watermarks.
5. On checkpoint, runtime persists the latest `Offset` in the checkpoint.
6. On shutdown, runtime calls `source.Close()`.

**Sink data flow (standard):**
1. Runtime calls `sink.Open(ctx, config)`.
2. Runtime batches events and calls `sink.WriteBatch(ctx, events)`.
3. On shutdown, runtime calls `sink.Close()`.

**Sink data flow (transactional, exactly-once):**
1. Runtime calls `sink.Open(ctx, config)` then `sink.BeginTransaction(ctx)`.
2. Runtime calls `sink.WriteBatch(ctx, events)` within the transaction.
3. On checkpoint barrier: `sink.PreCommit(ctx, checkpointID)` (Phase 1).
4. On global checkpoint complete: `sink.Commit(ctx, checkpointID)` (Phase 2).
5. New cycle: `sink.BeginTransaction(ctx)`.
6. On failure: `sink.Abort(ctx)`, then restore and restart.

---

## 3. API Design

### 3.1 Source Interface

```go
type Source interface {
    // Open initializes the source connection.
    Open(ctx context.Context, config SourceConfig) error

    // ReadBatch reads the next batch of events. Blocks until data available.
    ReadBatch(ctx context.Context, maxBatch int) ([]Event, Offset, error)

    // RestoreOffset rewinds to a previously saved offset (for recovery).
    RestoreOffset(ctx context.Context, offset Offset) error

    // GenerateWatermark returns the current watermark timestamp.
    GenerateWatermark() int64

    // Close releases all resources.
    Close() error
}

type Offset []byte

type SourceConfig struct {
    OperatorID   string
    SubtaskIndex int   // Which parallel instance (0..parallelism-1)
    Parallelism  int   // Total parallel instances
}
```

### 3.2 Sink Interface

```go
type Sink interface {
    Open(ctx context.Context, config SinkConfig) error
    WriteBatch(ctx context.Context, events []Event) error
    Close() error
}

type SinkConfig struct {
    OperatorID   string
    SubtaskIndex int
    Parallelism  int
}
```

### 3.3 TransactionalSink Interface

```go
type TransactionalSink interface {
    Sink
    BeginTransaction(ctx context.Context) error
    PreCommit(ctx context.Context, checkpointID int64) error
    Commit(ctx context.Context, checkpointID int64) error
    Abort(ctx context.Context) error
}
```

### 3.4 Connector Registry

```go
// Registration (typically in init())
func RegisterSource(typeName string, factory func() Source)
func RegisterSink(typeName string, factory func() Sink)

// Lookup (used by YAML parser and runtime)
func NewSource(typeName string) (Source, error)
func NewSink(typeName string) (Sink, error)
```

### 3.5 Built-in Connector Configurations

#### Kafka Source

```yaml
type: kafka
config:
  brokers: ["broker1:9092", "broker2:9092"]   # Required
  topic: "events"                              # Required
  group_id: "wire-consumer-group"              # Required
  start_offset: "latest"                       # earliest | latest | timestamp
  start_timestamp: "2024-01-01T00:00:00Z"     # When start_offset=timestamp
  max_batch_size: 500
  fetch_max_bytes: 1048576                     # 1MB
  session_timeout: "30s"
  heartbeat_interval: "3s"
```

#### Kafka Sink

```yaml
type: kafka
config:
  brokers: ["broker1:9092"]                    # Required
  topic: "output-events"                       # Required
  compression: "snappy"                        # none | gzip | snappy | lz4 | zstd
  acks: "all"                                  # 0 | 1 | all
  batch_size: 16384
  linger_ms: 5
  idempotent: true
  transactional_id: "wire-tx-"                 # Prefix (enables exactly-once)
```

#### PostgreSQL Sink

```yaml
type: postgresql
config:
  connection: "postgres://user:pass@host:5432/db?sslmode=require"  # Required
  table: "events"                              # Required
  columns:
    - event_field: "user_id"
      column: "user_id"
      type: "TEXT"
    - event_field: "count"
      column: "event_count"
      type: "INTEGER"
  batch_size: 1000
  on_conflict: "DO NOTHING"                    # Upsert behavior
```

#### Elasticsearch Sink

```yaml
type: elasticsearch
config:
  urls: ["http://es1:9200"]                    # Required (or cloud_id)
  index_name: "events"                         # Required
  cloud_id: ""
  api_key: ""
  username: ""
  password: ""
  batch_size: 500
  flush_interval: "1s"
  document_id_field: "id"                      # For idempotent writes
```

#### MongoDB Source

```yaml
type: mongodb
config:
  uri: "mongodb://host:27017"                  # Required
  database: "mydb"                             # Required
  collection: "events"                         # Required
  batch_size: 500
  full_document: "updateLookup"
```

#### MongoDB Sink

```yaml
type: mongodb
config:
  uri: "mongodb://host:27017"
  database: "mydb"
  collection: "output"
  batch_size: 1000
  ordered: false
  upsert_key: "_id"                            # For idempotent writes
```

#### S3 Sink

```yaml
type: s3
config:
  bucket: "my-data-lake"                       # Required
  region: "us-east-1"                          # Required
  prefix: "raw/events/"
  format: "json"                               # json | parquet | csv
  compression: "gzip"                          # none | gzip | snappy | zstd
  partition_by: ["date", "hour"]               # Hive-style partitioning
  file_size_mb: 128
  roll_interval: "1h"
  endpoint: ""                                 # Custom (MinIO, LocalStack)
```

#### Redis Sink

```yaml
type: redis
config:
  address: "localhost:6379"                    # Required
  password: ""
  db: 0
  key_field: "id"
  key_prefix: "wire:"
  command: "SET"                               # SET | HSET | LPUSH | RPUSH | PUBLISH
  ttl: "24h"
  batch_size: 100
```

#### HTTP Source

```yaml
type: http
config:
  address: ":8080"
  path: "/ingest"
  method: "POST"
  max_body_size: "10MB"
  auth:
    type: "bearer"                             # none | bearer | basic
    token: "${HTTP_AUTH_TOKEN}"
```

#### HTTP Sink

```yaml
type: http
config:
  url: "https://api.example.com/events"        # Required
  method: "POST"
  headers:
    Content-Type: "application/json"
    Authorization: "Bearer ${API_TOKEN}"
  batch_size: 100
  timeout: "30s"
  retry:
    max_attempts: 3
    backoff: "exponential"
    initial_delay: "1s"
    max_delay: "30s"
```

#### File Source

```yaml
type: file
config:
  path: "/data/input/*.json"                   # Glob pattern
  format: "json"                               # json | csv | line
  poll_interval: "5s"
  read_mode: "tail"                            # full | tail
```

#### File Sink

```yaml
type: file
config:
  path: "/data/output/"
  format: "json"
  compression: "gzip"
  file_size_mb: 256
  roll_interval: "1h"
  naming: "part-{subtask}-{sequence}.json.gz"
```

#### SQS Source

```yaml
type: sqs
config:
  queue_url: "https://sqs.us-east-1.amazonaws.com/123/queue"  # Required
  region: "us-east-1"
  max_messages: 10
  wait_time_seconds: 20
  visibility_timeout: 60
```

#### RabbitMQ Source

```yaml
type: rabbitmq
config:
  uri: "amqp://user:pass@host:5672/vhost"     # Required
  queue: "events"                              # Required
  exchange: ""
  routing_key: "#"
  prefetch_count: 100
  auto_ack: false
```

#### RabbitMQ Sink

```yaml
type: rabbitmq
config:
  uri: "amqp://user:pass@host:5672/vhost"
  exchange: "output"
  routing_key: "events.processed"
  persistent: true
```

#### Webhook Source

```yaml
type: webhook
config:
  address: ":9090"
  path: "/webhook"
  secret: "${WEBHOOK_SECRET}"
  signature_header: "X-Signature"
```

#### Webhook Sink

```yaml
type: webhook
config:
  url: "https://hooks.example.com/wire"
  headers:
    X-Wire-Source: "my-pipeline"
  timeout: "10s"
  retry:
    max_attempts: 5
    backoff: "exponential"
    initial_delay: "500ms"
```

---

## 4. Data Model & Storage

### 4.1 Connector Classification

| Connector | Source | Sink | Replayable | Transactional Sink | Idempotent Sink |
| -- | -- | -- | -- | -- | -- |
| Kafka | Yes | Yes | Yes (offsets) | Yes (transactions) | No |
| PostgreSQL | No | Yes | — | Yes (SQL transactions) | Via ON CONFLICT |
| Elasticsearch | No | Yes | — | No | Yes (doc ID) |
| MongoDB | Yes (change streams) | Yes | Yes (resume token) | No | Yes (upsert) |
| S3 | No | Yes | — | Yes (multipart commit) | No |
| Redis | No | Yes | — | No | Yes (SET) |
| HTTP | Yes (receiver) | Yes | No | No | No |
| File | Yes | Yes | Yes (byte offset) | No | No |
| SQS | Yes | No | Yes (visibility) | — | — |
| RabbitMQ | Yes | Yes | Yes (ack) | No | No |
| Webhook | Yes (receiver) | Yes | No | No | No |

### 4.2 Storage Considerations

* **Source offsets:** Serialized as `Offset` (opaque `[]byte`) and stored in checkpoint state alongside operator state.
* **Sink transaction state:** Transaction IDs tracked in coordinator metadata for crash recovery during 2PC.

---

## 5. Design Decisions & Trade-offs

### Decision 1: `ReadBatch` returns `Offset` (not stored internally)

|  |  |
| -- | -- |
| **Context** | Source needs to track position for checkpoint/recovery. |
| **Options Considered** | (A) Source returns offset per batch, runtime stores it; (B) Source manages its own offset checkpointing; (C) Offset stored in Pebble keyed state |
| **Decision** | Option A |
| **Rationale** | Clean separation of concerns. Source focuses on reading, runtime focuses on checkpointing. Offset is just another piece of checkpoint state. |
| **Trade-offs Accepted** | Source must be stateless between calls — cannot assume internal state survives recovery. |
| **Revisit Trigger** | If sources need complex multi-part offsets that don't serialize well to `[]byte`. |

### Decision 2: TransactionalSink as separate interface (not baked into Sink)

|  |  |
| -- | -- |
| **Context** | Not all sinks support transactions. Forcing all sinks to implement 2PC methods is wasteful. |
| **Options Considered** | (A) Single Sink interface with optional no-op transaction methods; (B) Separate TransactionalSink interface via Go interface embedding |
| **Decision** | Option B |
| **Rationale** | Clean type system. Runtime does `if ts, ok := sink.(TransactionalSink); ok { ... }`. No-op methods are error-prone. |
| **Trade-offs Accepted** | Two interfaces to document and test. |
| **Revisit Trigger** | If the number of sink interface variants grows beyond 2. |

### Decision 3: `[]byte` configuration (not structured Go config)

|  |  |
| -- | -- |
| **Context** | Connectors need configuration, but each connector has different fields. |
| **Options Considered** | (A) Each connector defines its own Go config struct; (B) Generic `map[string]interface{}` config; (C) YAML config parsed by each connector |
| **Decision** | Option A with YAML unmarshaling |
| **Rationale** | Strong typing with validation. YAML tags provide schema documentation. Each connector's config struct is its own documentation. |
| **Trade-offs Accepted** | New connector = new config struct. Cannot dynamically add config fields. |
| **Revisit Trigger** | If a plugin/dynamic loading system is added. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | Kafka broker unavailable during ReadBatch | ReadBatch blocks/retries per Kafka client config. Context cancellation forces return. | Source stalls, backpressure propagates | Medium |
| 2 | Postgres connection drops during PreCommit | PreCommit returns error. Checkpoint fails. Job restarts from last checkpoint. | Temporary delay | Medium |
| 3 | S3 multipart upload fails during Commit | Commit returns error. Job fails and restarts. Incomplete multipart uploads cleaned by S3 lifecycle policy. | Data delay | Medium |
| 4 | SQS visibility timeout expires before checkpoint | Messages redelivered. At-least-once semantics apply. | Duplicate processing | Low |
| 5 | HTTP Source receives burst exceeding buffer | Backpressure via bounded channel. HTTP returns 429 Too Many Requests. | Sender retries | Low |
| 6 | MongoDB change stream resume token expired | RestoreOffset fails. Job must start from earliest available. | Data gap possible | High |
| 7 | TransactionalSink Commit succeeds but ACK lost | On restart, Commit is called again with same checkpointID. Sink must handle idempotent Commit. | No impact if idempotent | Medium |

---

## 7. Security & Compliance

### 7.1 Authentication & Authorization

* All connector credentials support `${ENV_VAR}` substitution — never stored in plain text in pipeline configs.
* Kafka: SASL/SCRAM, SASL/PLAIN, mTLS supported via connector config.
* PostgreSQL: SSL/TLS via connection string parameters.
* Elasticsearch: API key, basic auth, or cloud ID.
* AWS (S3, SQS): IAM role (preferred) or explicit access key/secret key.
* MongoDB: Connection string with auth credentials.

### 7.2 Data Protection

* **In transit:** All connectors use TLS by default where the external system supports it.
* **Credentials at rest:** Stored only in environment variables or secret management systems (see WIP-09).

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Interface contracts, config parsing, serialization | Go `testing` | >= 80% per connector |
| Integration Tests | Each connector against real external system | Docker Compose + testcontainers | All connectors |
| Contract Tests | Source replay correctness, Sink idempotency | Custom test harness | All replayable sources, all idempotent sinks |
| Chaos Tests | Connection drops, timeout, partial writes | toxiproxy | Kafka, Postgres, Elasticsearch |

### 8.1 Key Test Scenarios

1. Kafka source: read → checkpoint → kill → restore → verify no duplicates/gaps
2. Postgres sink: write → PreCommit → Commit → verify rows
3. Postgres sink: write → PreCommit → Abort → verify rollback
4. S3 sink: write → Commit → verify complete file; Abort → verify no partial files
5. Elasticsearch sink: write with document_id → write same again → verify idempotent
6. Custom connector: implement Source, register, verify it works via YAML pipeline

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should connectors support schema evolution (e.g., Avro schema registry)? | Tarun | Open |
| 2 | How does parallel source assignment work for Kafka (partition assignment vs. topic assignment)? | Tarun | Open |
| 3 | Should we support "exactly-once" for S3 via rename-on-commit pattern? | Tarun | Open |
| 4 | Risk: MongoDB change stream resume tokens have a limited lifetime. What's the fallback? | — | Acknowledged |
| 5 | Should connector config support hot-reload without job restart? | Tarun | Open |
