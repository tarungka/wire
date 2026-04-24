# 📖 **Technical Documentation: `Wire`**

---

## 1️⃣ Overview & Purpose

**High-Level Summary:** Wire is a distributed, open-source stream processing framework engineered for real-time data flow handling. It provides a fault-tolerant pipeline architecture built on Raft consensus, enabling scalable data transformations between diverse sources and sinks with guaranteed consistency.

**Key Features:**
- Distributed consensus via HashiCorp Raft for fault tolerance
- Pipeline-based stream processing with partitioning support
- Pluggable source/sink architecture (MongoDB, Kafka, Elasticsearch, File)
- HTTP API for cluster management and data operations (Gin framework)
- Built-in support for data transformations and operation chaining
- Automatic leader election and cluster membership management
- Multi-database backend support (BadgerDB, BoltDB, RocksDB)
- Job-based processing with UUID v7 identification

---

## 2️⃣ Core Concepts & Architecture

**Architectural Pattern:** The codebase follows a **Microservices Architecture** with **Pipeline Pattern** for data processing and **Distributed Consensus Pattern** for state management. The structure supports horizontal scaling through Raft clustering.

**Main Components:**
- **NodeStore (FSM):** Complete Raft FSM implementation with multi-database backend support
- **Pipeline Engine:** Orchestrates data flow with operation chaining and transformations
- **HTTP Service:** Gin-based RESTful API for cluster management and pipeline operations
- **Transport Layer:** TCP multiplexing for efficient inter-node communication
- **Job Processor:** Partitioned, parallel processing with UUID v7 job identification
- **Database Abstraction:** Pluggable storage backends (BadgerDB, BoltDB, RocksDB)

**Data Flow Architecture:**
```
Source (Kafka/MongoDB) → Pipeline → Transform → Job Processor → Sink (ES/Kafka/File)
                             ↓
                        Raft Store (State)
```

---

## 3️⃣ Detailed Package/File Breakdown

### File: `/internal/new/db/db.go`

**Purpose:** Database abstraction layer for multiple backend support

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `DbStore` | Database interface | `Get()`, `Set()`: KV operations<br>`FirstIndex()`, `LastIndex()`: Log operations<br>`Close()`: Cleanup |

**Supported Backends:**
- **BadgerDB**: Default for FSM storage, LSM-tree based
- **BoltDB**: Via raft-boltdb, B+tree based
- **RocksDB**: High-performance LSM-tree (requires CGO)

### File: `/sinks/file.go`

**Purpose:** File-based sink for pipeline output

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `FileSink` | File writer | `file *os.File`: Output file<br>`encoder *json.Encoder`: JSON writer<br>`Write()`: Concurrent write |

**Key Features:**
- Buffered writing with OS-level buffering
- JSON encoding for structured data
- Context-aware cancellation
- Thread-safe concurrent writes

### File: `/cmd/main.go`

**Purpose:** Application entry point orchestrating service initialization and lifecycle

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Config` | Main configuration | `HTTPAddr string`: HTTP bind address<br>`RaftAddr string`: Raft bind address<br>`JoinAddr []string`: Cluster join addresses<br>`StoreDatabase string`: Backend DB type |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `main()` | Application bootstrap | None<br>No return | `log.Fatal()` on critical errors |
| `startNodeMux()` | TCP multiplexer setup | `tcp *tcp.Mux`<br>Returns `net.Listener` | Returns error if binding fails |
| `createStore()` | Initialize Raft store | `cfg *Config`, `ly *tcp.Layer`<br>Returns `*store.NodeStore, error` | Propagates store creation errors |
| `createCluster()` | Bootstrap/join cluster | Extended params with context<br>Returns `error` | Handles bootstrap conflicts |
| `startHTTPService()` | Start HTTP API | Includes `context.Context`<br>Returns `*http.Service, error` | Returns service start errors |

**Global Variables & Constants:**
- `ko`: Koanf configuration manager
- `logo`: ASCII art branding
- Signal channels for graceful shutdown

### File: `/cmd/init.go`

**Purpose:** Configuration parsing, validation, and initialization logic

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Config` | Extended configuration | `DiscoveryMode string`: DNS/Consul/etcd<br>`Validate()`: Config validation<br>`LoadCertificates()`: TLS setup |
| `StringSliceValue` | Custom flag type | `Set()`: Parse comma-separated values |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `initFlags()` | Parse CLI flags | `*flag.FlagSet`<br>Returns `*Config, error` | Returns parsing errors |
| `Validate()` | Validate config | None<br>Returns `error` | Comprehensive validation errors |
| `CheckFilePaths()` | Validate file paths | `paths []string`<br>Returns `error` | Returns if files don't exist |

**Global Variables & Constants:**
- Discovery modes: `DNS`, `DNS-SRV`, `CONSUL`, `ETCD`
- Default addresses and ports
- Certificate path defaults

### File: `/internal/new/store/store.go`

**Purpose:** Core distributed store implementing complete Raft FSM with multi-database backend support

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `NodeStore` | Main store struct | `raft *raft.Raft`: Consensus engine<br>`db *badgerdb.DB`: FSM storage<br>`dbStore db.DbStore`: Backend storage<br>`storeDb string`: Backend type |
| `Config` | Store configuration | `Dir string`: Working directory<br>`ID string`: Node identifier<br>`StoreDatabase string`: Backend DB type |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Apply()` | FSM log apply | `*raft.Log`<br>Returns `interface{}` | Handles SET/DELETE/NOOP |
| `Snapshot()` | Create FSM snapshot | None<br>Returns `raft.FSMSnapshot, error` | Full implementation with metadata |
| `Bootstrap()` | Initialize cluster | `servers []raft.Server`<br>Returns `error` | Validates configuration |
| `Join()` | Add node to cluster | `raftID, httpAddr, addr string`<br>Returns `error` | Leader-only operation |
| `StoreInDatabase()` | Write key-value | `key, value string`<br>Returns `error` | Raft consensus errors |
| `GetFromDatabase()` | Read key-value | `key string`<br>Returns `string, error` | Direct BadgerDB read |

**Global Variables & Constants:**
- `ErrStoreNotOpen`: Store not initialized error
- `ErrNotLeader`: Non-leader write attempt
- FSM operation types: SET, DELETE, NOOP

### File: `/internal/new/store/snapshot.go`

**Purpose:** Complete snapshot implementation for FSM state persistence

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `fsmSnapshot` | Snapshot implementation | `store *NodeStore`: Parent store<br>`Persist()`: Write snapshot<br>`Release()`: Cleanup |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Snapshot()` | Create snapshot | None<br>Returns `raft.FSMSnapshot, error` | Creates BadgerDB backup |
| `Persist()` | Write snapshot | `sink raft.SnapshotSink`<br>No return | Handles backup errors |
| `Restore()` | Restore state | `io.ReadCloser`<br>Returns `error` | Restores from backup |

### File: `/internal/pipeline/pipeline.go`

**Purpose:** Core data pipeline orchestration with operation chaining and transformations

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `DataPipeline` | Pipeline controller | `source DataSource`: Input source<br>`sink DataSink`: Output sink<br>`operations []*PipelineOps`: Transform chain |
| `PipelineOps` | Operation node | `id string`: Unique ID<br>`operation Operation`: Transform<br>`parents/children []*PipelineNode`: DAG links |
| `Operation` | Operation interface | `Process(*models.Job) (*models.Job, error)`: Transform |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Run()` | Execute pipeline | `ctx context.Context`<br>No return | Context cancellation |
| `AddOperation()` | Add transform | `op Operation`<br>No return | Chains operations |
| `runOperation()` | Process jobs | `ctx context.Context, op *PipelineOps`<br>No return | Logs and continues |

### File: `/internal/models/job.go`

**Purpose:** Core job model with UUID v7 identification and thread-safe operations

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Job` | Job structure | `ID uuid.UUID`: UUID v7 identifier<br>`data any`: Payload<br>`eventTime time.Time`: Event timestamp<br>`mu sync.RWMutex`: Thread safety |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `New()` | Create job | `data []byte`<br>Returns `*Job, error` | JSON parsing errors |
| `Data()` | Get data | None<br>Returns `any` | Thread-safe read |
| `SetData()` | Set data | `data any`<br>No return | Thread-safe write |
| `parseEventTime()` | Extract time | `jsonData map[string]any`<br>Returns `time.Time` | Returns current time on error |

### File: `/internal/http/service.go`

**Purpose:** Gin-based HTTP API service for cluster management and pipeline operations

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Service` | HTTP server | `Context context.Context`: App context<br>`store Store`: NodeStore<br>`Start(ctx)`: Launch server |
| `Store` | Store interface | `StoreInDatabase()`: Write KV<br>`GetFromDatabase()`: Read KV<br>`Nodes()`: Get cluster nodes |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Start()` | Start HTTP server | `ctx context.Context`<br>Returns `error` | Server startup errors |
| `handleStatus()` | Cluster status | `gin.Context`<br>No return | HTTP 500 on errors |
| `createPipeline()` | Create pipeline | `gin.Context`<br>No return | HTTP 400 on parse errors |
| `handleConnector()` | Pipeline routes | `gin.Context`<br>No return | Route to handlers |

**Global Variables & Constants:**
- `VersionHTTPHeader`: X-WIRE-VERSION
- `ServedByHTTPHeader`: X-WIRE-SERVED-BY
- Queue processing with goroutines

---

## 4️⃣ Data Flow & Execution

**Startup Sequence:**
1. Parse command-line flags and validate configuration
2. Initialize logging with zerolog
3. Create TCP multiplexer for node communication
4. Initialize BadgerDB and Raft store
5. Bootstrap new cluster or join existing one
6. Start HTTP API service
7. Initialize pipeline system if configured
8. Handle shutdown signals gracefully

**Request Flow:**
```
HTTP Request → Gin Router → Handler → Store/Pipeline → BadgerDB/Raft
                                ↓
                          Leader Check → Forward if follower
```

**Pipeline Processing Flow:**
1. Source emits data records
2. Pipeline transforms data through operations
3. JobProcessor partitions work across goroutines
4. Transformed data written to configured sinks
5. State persisted through Raft consensus

---

## 5️⃣ Dependencies

**Standard Library:**
- `net/http`: HTTP server foundation
- `context`: Cancellation and timeouts
- `sync`: Concurrency primitives
- `encoding/json`: Data serialization
- `flag`: CLI parsing
- `os/signal`: Graceful shutdown

**Internal Packages:**
- `internal/cluster`: Cluster management
- `internal/tcp`: Network multiplexing
- `internal/models`: Job data structures
- `internal/transform`: Data transformations
- `internal/new/db`: Database abstraction layer
- `internal/pipeline`: Pipeline orchestration

**Third-Party Libraries:**
- `github.com/hashicorp/raft`: Distributed consensus
- `github.com/dgraph-io/badger/v4`: Embedded key-value store
- `github.com/gin-gonic/gin`: HTTP framework
- `github.com/rs/zerolog`: Structured logging
- `github.com/twmb/franz-go`: Kafka client
- `go.mongodb.org/mongo-driver`: MongoDB client
- `github.com/elastic/go-elasticsearch/v8`: Elasticsearch client
- `github.com/knadh/koanf/v2`: Configuration management
- `go.etcd.io/bbolt`: BoltDB for Raft log storage
- `github.com/google/uuid`: UUID v7 generation

---

## 6️⃣ Usage Example

```go
// Example: Starting a Wire node with backend selection
package main

import (
    "log"
    "github.com/tarungka/wire/cmd"
)

func main() {
    // Single node bootstrap with BadgerDB backend
    cfg := &cmd.Config{
        HTTPAddr:      "localhost:8080",
        RaftAddr:      "localhost:8089", 
        NodeID:        "node1",
        DataPath:      "/var/wire/data",
        StoreDatabase: "badgerdb", // or "bbolt", "rocksdb"
        Bootstrap:     true,
    }
    
    // Configure pipeline via HTTP API
    pipelineConfig := `{
        "source": {
            "name": "Kafka Events",
            "type": "kafka", 
            "key": "events_pipeline",
            "config": {
                "brokers": ["localhost:9092"],
                "topic": "events",
                "consumer_group": "wire_consumer"
            }
        },
        "sink": {
            "name": "Events File",
            "type": "file",
            "key": "events_pipeline", 
            "config": {
                "path": "/var/wire/output/events.json"
            }
        }
    }`
}
```

---

## 7️⃣ Design Rationale & Trade-offs

**Why Raft Consensus?**
- Provides strong consistency guarantees for distributed state
- Simpler to understand than Paxos
- Battle-tested implementation from HashiCorp
- Trade-off: Requires majority quorum, limiting availability during partitions

**Why Multiple Database Backends?**
- **BadgerDB**: Default for FSM, pure Go, LSM-tree optimized for SSDs
- **BoltDB**: Lower memory usage, B+tree design, stable for small datasets
- **RocksDB**: Maximum performance, requires CGO, best for large datasets
- Trade-off: Complexity vs flexibility for different deployment scenarios

**Why Pipeline Architecture?**
- Composable data transformations
- Easy to extend with new operations
- Natural parallelism through partitioning
- Trade-off: Added complexity for simple use cases

**Why TCP Multiplexing?**
- Single port for multiple protocols (Raft, RPC)
- Reduced firewall configuration
- Connection pooling efficiency
- Trade-off: Custom protocol handling complexity

**Interface Abstractions:**
- `DbStore` interface for pluggable storage backends
- `Operation` interface enables custom transforms
- `DataSource`/`DataSink` interfaces for extensibility
- Trade-off: Additional indirection layers vs flexibility

**Why Gin Framework?**
- Better routing and middleware support
- Built-in JSON validation and binding
- Performance optimizations
- Trade-off: Additional dependency vs standard library

---

## 8️⃣ Potential Pitfalls & TODOs

**Current TODOs:**
- Complete TLS/mTLS configuration for production security
- Add metrics collection beyond basic expvar
- Implement additional sources (Pulsar, RabbitMQ)
- Add transaction support for multi-key operations
- Implement pipeline state persistence and recovery
- Add support for exactly-once semantics

**Potential Improvements:**
1. **Error Handling:** Wrap errors with context using `fmt.Errorf("operation failed: %w", err)`
2. **Performance:** Implement connection pooling for sink writers
3. **Testing:** Add integration tests for cluster scenarios
4. **Monitoring:** Integrate OpenTelemetry for distributed tracing
5. **Security:** Add rate limiting to HTTP endpoints
6. **Operations:** Implement graceful leader transfer during maintenance

**Known Limitations:**
- No built-in data schema validation
- Limited to single-region deployments (no geo-replication)
- HTTP API lacks pagination for large result sets
- Pipeline operations are in-memory only (no persistence)
- No support for complex event processing (CEP)
- Job prioritization not yet implemented

---

## ✅ **Deliverable**

This technical documentation provides a comprehensive overview of the Wire distributed stream processing framework. The codebase demonstrates professional-grade engineering with clear separation of concerns, robust error handling, and a focus on reliability through distributed consensus. The architecture supports horizontal scaling and provides a solid foundation for building real-time data pipelines.