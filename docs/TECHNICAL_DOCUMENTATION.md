# 📖 **Technical Documentation: `Wire`**

---

## 1️⃣ Overview & Purpose

**High-Level Summary:** Wire is a distributed, open-source stream processing framework engineered for real-time data flow handling. It provides a fault-tolerant pipeline architecture built on Raft consensus, enabling scalable data transformations between diverse sources and sinks with guaranteed consistency.

**Key Features:**
- Distributed consensus via HashiCorp Raft for fault tolerance
- Pipeline-based stream processing with partitioning support
- Pluggable source/sink architecture (MongoDB, Kafka, Elasticsearch, File)
- HTTP API for cluster management and data operations
- Built-in support for data transformations
- Automatic leader election and cluster membership management
- Persistent state storage with BadgerDB

---

## 2️⃣ Core Concepts & Architecture

**Architectural Pattern:** The codebase follows a **Microservices Architecture** with **Pipeline Pattern** for data processing and **Distributed Consensus Pattern** for state management. The structure supports horizontal scaling through Raft clustering.

**Main Components:**
- **Raft Store (FSM):** Distributed state machine providing consensus and persistence
- **Pipeline Engine:** Orchestrates data flow from sources through transformations to sinks
- **HTTP Service:** RESTful API for cluster management and operations
- **Transport Layer:** TCP multiplexing for efficient inter-node communication
- **Job Processor:** Partitioned, parallel processing of data streams

**Data Flow Architecture:**
```
Source (Kafka/MongoDB) → Pipeline → Transform → Job Processor → Sink (ES/Kafka/File)
                             ↓
                        Raft Store (State)
```

---

## 3️⃣ Detailed Package/File Breakdown

### File: `/cmd/main.go`

**Purpose:** Application entry point orchestrating service initialization and lifecycle

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Config` | Main configuration | `HTTPAddr string`: HTTP bind address<br>`RaftAddr string`: Raft bind address<br>`JoinAddr []string`: Cluster join addresses |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `main()` | Application bootstrap | None<br>No return | `log.Fatal()` on critical errors |
| `startNodeMux()` | TCP multiplexer setup | `tcp *tcp.Mux`<br>Returns `net.Listener` | Returns error if binding fails |
| `createStore()` | Initialize Raft store | `cfg *Config`, listeners<br>Returns `*store.NodeStore, error` | Propagates store creation errors |
| `createCluster()` | Bootstrap/join cluster | `cfg *Config`, `str *store.NodeStore`<br>Returns `error` | Handles bootstrap conflicts |
| `startHTTPService()` | Start HTTP API | Multiple params<br>Returns `*http.Service, error` | Returns service start errors |

**Global Variables & Constants:**
- `version`: Build version string
- `metadata`: Platform/arch metadata
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

**Purpose:** Core distributed store implementing Raft FSM and storage interfaces

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `NodeStore` | Main store struct | `raft *raft.Raft`: Consensus engine<br>`db *badger.DB`: Data storage<br>`raftConfig *raft.Config`: Raft configuration |
| `Config` | Store configuration | `DBPath string`: Database directory<br>`RaftLogEngine string`: Log storage type<br>`ReplicationFactor int`: Cluster size |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Apply()` | FSM log apply | `*raft.Log`<br>Returns `interface{}` | Returns command errors |
| `Snapshot()` | Create FSM snapshot | None<br>Returns `raft.FSMSnapshot, error` | Currently returns `ErrNotImplemented` |
| `Bootstrap()` | Initialize cluster | `servers []Server`<br>Returns `error` | Validates single-node bootstrap |
| `Join()` | Add node to cluster | `raftID, httpAddr, tcpAddr string`<br>Returns `error` | Leader-only operation |
| `StoreInDatabase()` | Write key-value | `key, value string`<br>Returns `error` | BadgerDB transaction errors |
| `GetFromDatabase()` | Read key-value | `key string`<br>Returns `string, error` | Returns empty if not found |

**Global Variables & Constants:**
- `ErrNotImplemented`: Placeholder for unimplemented features
- `WaitDequeue`: 5-second timeout
- Default Raft timeouts and thresholds

### File: `/internal/new/store/fsm.go`

**Purpose:** Finite State Machine implementation for Raft consensus

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| FSM interface methods | Raft FSM contract | `Apply()`: Process log entries<br>`Snapshot()`: Create state snapshot<br>`Restore()`: Restore from snapshot |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Apply()` | Apply log entry | `log *raft.Log`<br>Returns `interface{}` | Handles BadgerDB errors |
| `Snapshot()` | Create snapshot | None<br>Returns `raft.FSMSnapshot, error` | Returns `ErrNotImplemented` |
| `Restore()` | Restore state | `io.ReadCloser`<br>Returns `error` | Returns `ErrNotImplemented` |

### File: `/internal/pipeline/pipeline.go`

**Purpose:** Core data pipeline orchestration with DAG-based operations

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `DataPipeline` | Pipeline controller | `nodes []*PipelineNode`: Operation DAG<br>`jobs chan *models.Job`: Job queue<br>`Run()`: Execute pipeline |
| `PipelineOps` | Operation metadata | `ID string`: Unique identifier<br>`Type string`: Operation type<br>`Process()`: Execute operation |
| `Operation` | Operation interface | `Process()`: Transform data |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Run()` | Execute pipeline | `ctx context.Context`<br>No return | Context cancellation |
| `AddOperation()` | Add pipeline op | `op Operation`<br>No return | None |
| `processJobs()` | Process job queue | `ctx context.Context`<br>No return | Logs errors, continues |

### File: `/internal/pipeline/processor.go`

**Purpose:** Parallel job processing with partitioning support

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `JobProcessor` | Job processor | `partitions int`: Parallelism level<br>`Process()`: Execute jobs<br>`writeToSink()`: Output handler |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Process()` | Process job batch | `jobs []*models.Job`<br>No return | Logs sink write errors |
| `writeToSink()` | Write to output | `job *models.Job`<br>Returns `error` | Returns sink errors |

### File: `/internal/http/service.go`

**Purpose:** HTTP API service for cluster management and operations

**Key Types (Structs & Interfaces):**

| Name | Description | Key Fields / Methods |
|------|-------------|---------------------|
| `Service` | HTTP server | `router *gin.Engine`: Web framework<br>`store Store`: Raft store<br>`Start()`: Launch server |
| `Store` | Store interface | `Join()`, `Notify()`: Cluster ops<br>`Query()`, `Execute()`: Data ops |

**Key Functions/Methods:**

| Function/Method | Description | Parameters & Returns | Error Handling |
|-----------------|-------------|---------------------|----------------|
| `Start()` | Start HTTP server | None<br>Returns `error` | Server startup errors |
| `StatusHandler()` | Cluster status | `gin.Context`<br>No return | HTTP 500 on errors |
| `JoinHandler()` | Join cluster | `gin.Context`<br>No return | HTTP 400/500 on errors |
| `KeyHandler()` | KV operations | `gin.Context`<br>No return | HTTP 400/404/500 errors |

**Global Variables & Constants:**
- Queue capacity: 16 for write operations
- Default timeouts for operations

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
- `internal/models`: Data structures
- `internal/transform`: Data transformations

**Third-Party Libraries:**
- `github.com/hashicorp/raft`: Distributed consensus
- `github.com/dgraph-io/badger/v4`: Embedded key-value store
- `github.com/gin-gonic/gin`: HTTP framework
- `github.com/rs/zerolog`: Structured logging
- `github.com/twmb/franz-go`: Kafka client
- `go.mongodb.org/mongo-driver`: MongoDB client
- `github.com/elastic/go-elasticsearch/v8`: Elasticsearch client

---

## 6️⃣ Usage Example

```go
// Example: Starting a Wire node and joining a cluster
package main

import (
    "log"
    "github.com/yourusername/wire/cmd"
)

func main() {
    // Single node bootstrap
    cfg := &cmd.Config{
        HTTPAddr:   "localhost:8080",
        RaftAddr:   "localhost:8089",
        NodeID:     "node1",
        DataDir:    "/var/wire/data",
        Bootstrap:  true,
    }
    
    // Join existing cluster
    cfg2 := &cmd.Config{
        HTTPAddr:   "localhost:8081",
        RaftAddr:   "localhost:8090",
        NodeID:     "node2",
        DataDir:    "/var/wire/data2",
        JoinAddr:   []string{"localhost:8080"},
    }
    
    // Configure pipeline
    pipelineConfig := `{
        "sources": [{
            "type": "kafka",
            "config": {"brokers": ["localhost:9092"], "topic": "events"}
        }],
        "sinks": [{
            "type": "elasticsearch",
            "config": {"url": "http://localhost:9200", "index": "events"}
        }]
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

**Why BadgerDB?**
- Pure Go implementation (no CGO dependencies)
- LSM-tree design optimized for SSDs
- Built-in compression and encryption
- Trade-off: Higher memory usage than BoltDB

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
- `Store` interface allows swapping consensus implementations
- `Operation` interface enables custom transforms
- `Source`/`Sink` interfaces for extensibility
- Trade-off: Additional indirection layers

---

## 8️⃣ Potential Pitfalls & TODOs

**Current TODOs:**
- `fsm.go`: Implement `Snapshot()` and `Restore()` methods (currently return `ErrNotImplemented`)
- Complete TLS/mTLS configuration for production security
- Add metrics collection beyond basic expvar
- Implement additional sources (Pulsar, RabbitMQ)
- Add transaction support for multi-key operations

**Potential Improvements:**
1. **Error Handling:** Wrap errors with context using `fmt.Errorf("operation failed: %w", err)`
2. **Performance:** Implement connection pooling for sink writers
3. **Testing:** Add integration tests for cluster scenarios
4. **Monitoring:** Integrate OpenTelemetry for distributed tracing
5. **Security:** Add rate limiting to HTTP endpoints
6. **Operations:** Implement graceful leader transfer during maintenance

**Known Limitations:**
- Snapshot/restore not implemented (limits large state recovery)
- No built-in data schema validation
- Limited to single-region deployments (no geo-replication)
- HTTP API lacks pagination for large result sets

---

## ✅ **Deliverable**

This technical documentation provides a comprehensive overview of the Wire distributed stream processing framework. The codebase demonstrates professional-grade engineering with clear separation of concerns, robust error handling, and a focus on reliability through distributed consensus. The architecture supports horizontal scaling and provides a solid foundation for building real-time data pipelines.