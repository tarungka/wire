# Wire - Low Level System Design

## Table of Contents
1. [Executive Summary](#executive-summary)
2. [System Architecture Overview](#system-architecture-overview)
3. [Core Components](#core-components)
4. [Data Flow and Processing Pipeline](#data-flow-and-processing-pipeline)
5. [Network and Communication Layer](#network-and-communication-layer)
6. [Storage and Persistence Layer](#storage-and-persistence-layer)
7. [Clustering and Distributed Consensus](#clustering-and-distributed-consensus)
8. [API and Service Interfaces](#api-and-service-interfaces)
9. [Security and Authentication](#security-and-authentication)
10. [Performance and Scalability](#performance-and-scalability)
11. [Deployment Architecture](#deployment-architecture)
12. [Monitoring and Observability](#monitoring-and-observability)

## Executive Summary

Wire is a high-performance, distributed stream processing framework built with Go, designed for real-time data ingestion, transformation, and distribution. The system employs a modular, plugin-based architecture with strong consistency guarantees through Raft consensus, efficient network communication via TCP multiplexing, and flexible data pipeline processing.

### Key Features
- **Distributed Architecture**: Multi-node cluster support with automatic failover
- **Stream Processing**: Real-time data pipeline with configurable sources and sinks
- **High Performance**: Concurrent processing with worker pools and efficient I/O
- **Fault Tolerance**: Raft-based consensus for data consistency
- **Extensible**: Plugin architecture for custom sources/sinks/transformers

### Technology Stack
- **Language**: Go 1.21+
- **Consensus**: HashiCorp Raft
- **Storage**: BadgerDB/RocksDB
- **API Framework**: Gin
- **Serialization**: Protocol Buffers
- **Logging**: Zerolog
- **Containerization**: Docker/Alpine Linux

## System Architecture Overview

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                           Wire Cluster                              │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐         │
│  │   Node 1     │     │   Node 2     │     │   Node 3     │         │
│  │  (Leader)    │◄────┤  (Follower)  │◄────┤  (Follower)  │         │
│  └──────┬───────┘     └──────┬───────┘     └──────┬───────┘         │
│         │                    │                     │                │
│         └────────────────────┴─────────────────────┘                │
│                         Raft Consensus                              │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │                    Data Pipeline Layer                   │       │
│  │  ┌─────────┐    ┌────────────┐    ┌──────────┐           │       │
│  │  │ Sources │───►│ Transform  │───►│  Sinks   │           │       │
│  │  └─────────┘    └────────────┘    └──────────┘           │       │
│  └──────────────────────────────────────────────────────────┘       │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │                  Service Layer                           │       │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐                │       │
│  │  │   HTTP   │  │ Cluster  │  │  Store   │                │       │
│  │  │ Service  │  │ Service  │  │ Service  │                │       │
│  │  └──────────┘  └──────────┘  └──────────┘                │       │
│  └──────────────────────────────────────────────────────────┘       │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Component Layers

1. **Application Layer**
   - Main entry point (`cmd/main.go`)
   - Configuration management
   - Service initialization and orchestration

2. **Service Layer**
   - HTTP Service: REST API endpoints
   - Cluster Service: Inter-node communication
   - Store Service: Data persistence and Raft

3. **Pipeline Layer**
   - Source connectors
   - Transform operations
   - Sink connectors
   - Worker pool management

4. **Infrastructure Layer**
   - Network multiplexing
   - Storage abstraction
   - Logging and monitoring

## Core Components

### 1. Main Application (`cmd/main.go`)

**Responsibilities:**
- Service initialization sequence
- Signal handling and graceful shutdown
- Configuration loading and validation
- Component lifecycle management

**Initialization Flow:**
```go
1. Handle signals (SIGINT, SIGTERM)
2. Create main context
3. Initialize logging
4. Parse configuration
5. Create network multiplexer
6. Initialize Raft layer
7. Create store service
8. Create cluster service
9. Start HTTP service
10. Open store and join/bootstrap cluster
```

**Key Structures:**
```go
type Config struct {
    NodeID                string
    DataPath              string
    RaftAddr              string
    RaftAdv               string
    HTTPAddr              string
    HTTPAdv               string
    StoreDatabase         string
    BootstrapExpect       int
    RaftNonVoter          bool
    RaftHeartbeatTimeout  time.Duration
    RaftElectionTimeout   time.Duration
    // ... additional fields
}
```

### 2. Store Service (`internal/new/store/`)

**Architecture:**
```
┌─────────────────────────────────────┐
│         Store Service API           │
├─────────────────────────────────────┤
│          FSM (Finite State Machine) │
├─────────────────────────────────────┤
│           Raft Consensus            │
├─────────────────────────────────────┤
│      Storage Backend (BadgerDB)     │
└─────────────────────────────────────┘
```

**Core Components:**

- **NodeStore**: Main store implementation
  - Location: `internal/new/store/store.go`
  - Manages Raft node lifecycle
  - Handles read/write operations
  - Snapshot management

- **FSM (Finite State Machine)**: State machine for Raft
  - Location: `internal/store/fsm.go`
  - Applies committed log entries
  - Manages snapshots
  - Handles restore operations

- **Storage Backends**:
  - BadgerDB: `internal/new/db/badgerdb/db.go`
  - RocksDB: `internal/new/db/rocksdb/db.go`

**Key Operations:**
```go
interface Store {
    // Write operations (leader only)
    StoreInDatabase(key, value string) error

    // Read operations
    GetFromDatabase(key string) (string, error)

    // Cluster operations
    Bootstrap(server *Server) error
    Remove(nodeID string) error

    // Status operations
    IsLeader() bool
    LeaderAddr() (string, error)
    Stats() (map[string]interface{}, error)
}
```

### 3. Pipeline Service (`internal/pipeline/`)

**Pipeline Architecture:**
```
Source → [Channel] → Transform → [Channel] → Sink
         ↓                ↓                    ↓
     Worker Pool      Worker Pool         Worker Pool
```

**Core Components:**

- **DataPipeline**: Main pipeline orchestrator
  ```go
  type DataPipeline struct {
      Source     DataSource
      Sink       DataSink
      operations []*PipelineOps
      cancel     context.CancelFunc
      jobCount   uint
  }
  ```

- **Job Model**: Data unit processed through pipeline
  ```go
  type Job struct {
      ID            uuid.UUID
      data          any
      nodeCreatedAt time.Time
      nodeUpdatedAt time.Time
      eventTime     time.Time
      priority      int
      mu            sync.RWMutex
  }
  ```

- **Worker Pool**: Concurrent processing
  - Configurable worker count
  - Channel-based job distribution
  - Back-pressure handling

### 4. HTTP Service (`internal/http/`)

**Service Structure:**
```go
type Service struct {
    addr       string
    store      Store
    cluster    Cluster
    router     *gin.Engine
    httpServer *http.Server
}
```

**API Endpoints:**

| Method | Path              | Description              |
|--------|-------------------|--------------------------|
| GET    | /health           | Health check             |
| GET    | /status           | Node status              |
| POST   | /db/execute       | Execute write operation  |
| GET    | /db/query         | Execute read query       |
| POST   | /cluster/join     | Join cluster             |
| DELETE | /cluster/remove   | Remove node              |
| GET    | /pipelines        | List pipelines           |
| POST   | /pipelines        | Create pipeline          |
| DELETE | /pipelines/:id    | Delete pipeline          |

### 5. Cluster Service (`internal/cluster/`)

**Cluster Communication:**
```
Node A                    Node B
  │                         │
  ├─[Raft Protocol]────────►│  Port: Raft (MuxRaftHeader)
  │                         │
  ├─[Cluster Protocol]─────►│  Port: Cluster (MuxClusterHeader)
  │                         │
  └─[HTTP API]─────────────►│  Port: HTTP
```

**Key Components:**

- **Service**: Handles cluster state and node communication
- **Client**: Makes requests to other nodes
- **Joiner**: Manages node joining process
- **Bootstrapper**: Handles cluster initialization

## Data Flow and Processing Pipeline

### Pipeline Execution Flow

```
1. Source Connection
   ↓
2. Data Ingestion
   ↓
3. Job Creation (UUID v7)
   ↓
4. Channel Queue
   ↓
5. Worker Assignment
   ↓
6. Transformation
   ↓
7. Result Queue
   ↓
8. Sink Writing
   ↓
9. Acknowledgment
```

### Source Connectors

**Available Sources:**

1. **Kafka Source** (`sources/kafka.go`)
   - Consumer group management
   - Offset tracking
   - Partition assignment

2. **MongoDB Source** (`sources/mongo.go`)
   - Change stream support
   - Collection monitoring
   - Resume token management

3. **File Source** (planned)
   - File watching
   - Line-by-line processing
   - Rotation handling

### Sink Connectors

**Available Sinks:**

1. **Elasticsearch Sink** (`sinks/elasticsearch.go`)
   - Bulk indexing
   - Index templates
   - Error handling with retry

2. **Kafka Sink** (`sinks/kafka.go`)
   - Producer configuration
   - Partitioning strategy
   - Compression support

3. **File Sink** (`sinks/file.go`)
   - Buffered writing
   - Rotation policies
   - Format customization

### Transform Operations

**Transform Pipeline:**
```go
type Operation interface {
    ID() string
    Process(ctx context.Context, in <-chan *models.Job) <-chan *models.Job
}
```

**Built-in Transforms:**
- JSON parsing/manipulation
- Field mapping
- Filtering
- Aggregation
- Custom functions

## Network and Communication Layer

### TCP Multiplexing (`internal/tcp/`)

**Multiplexer Architecture:**
```
┌────────────────────────────────┐
│      TCP Listener (Single)     │
├────────────────────────────────┤
│          Multiplexer           │
├──────────────┬─────────────────┤
│ Raft Stream  │ Cluster Stream  │
│  (Header 1)  │   (Header 2)    │
└──────────────┴─────────────────┘
```

**Implementation:**
```go
type Mux struct {
    ln        net.Listener
    addr      NameAddress
    handlers  map[byte]net.Listener
    tlsConfig *tls.Config
}
```

**Features:**
- Single port for multiple protocols
- Header-based routing
- TLS support
- Connection pooling

### Protocol Definitions

**Protocol Buffers (`internal/cluster/proto/`):**
```protobuf
message Command {
    Type type = 1;
    bytes sub_command = 2;
}

message JoinRequest {
    string id = 1;
    string address = 2;
    bool voter = 3;
}
```

## Storage and Persistence Layer

### Storage Architecture

```
┌─────────────────────────────┐
│     Application Layer       │
├─────────────────────────────┤
│    Database Interface       │
├─────────────────────────────┤
│   Storage Backend Driver    │
├─────────────────────────────┤
│    BadgerDB / RocksDB       │
└─────────────────────────────┘
```

### BadgerDB Integration

**Configuration:**
```go
type BadgerDB struct {
    db     *badger.DB
    path   string
    logger zerolog.Logger
}
```

**Key Features:**
- LSM-tree based storage
- Built-in compression
- Transaction support
- TTL support

### Snapshot Management

**Snapshot Process:**
1. FSM creates snapshot
2. Write to snapshot store
3. Compress with gzip
4. Store metadata
5. Cleanup old snapshots

**Snapshot Structure:**
```go
type Snapshot struct {
    data  []byte
    index uint64
    term  uint64
}
```

## Clustering and Distributed Consensus

### Raft Implementation

**Raft Configuration:**
```go
config := raft.DefaultConfig()
config.HeartbeatTimeout = 1000 * time.Millisecond
config.ElectionTimeout = 1000 * time.Millisecond
config.SnapshotThreshold = 8192
config.SnapshotInterval = 120 * time.Second
```

### Cluster Operations

**Node States:**
- **Leader**: Handles all writes, replicates to followers
- **Follower**: Replicates from leader, can serve reads
- **Candidate**: Temporary state during elections
- **Non-Voter**: Replicates but doesn't participate in voting

**Join Process:**
1. New node starts
2. Contacts existing cluster member
3. Sends join request with node info
4. Leader adds node to configuration
5. Node begins replication

**Bootstrap Process:**
```go
if bootstrapExpect > 0 {
    // Multi-node bootstrap
    waitForQuorum()
    electInitialLeader()
} else {
    // Single-node bootstrap
    bootstrap(self)
}
```

## API and Service Interfaces

### REST API Design

**Request Flow:**
```
Client → HTTP Handler → Validation → Service Layer → Store → Response
```

**API Response Format:**
```json
{
    "results": [...],
    "error": null,
    "time": 0.123
}
```

### Internal APIs

**Database Interface:**
```go
type Database interface {
    StoreInDatabase(key, value string) error
    GetFromDatabase(key string) (string, error)
    Nodes() ([]raft.Server, error)
    Stats() (map[string]interface{}, error)
}
```

**Cluster Interface:**
```go
type Cluster interface {
    GetNodeAPIAddr(addr string, retries int, timeout time.Duration) (string, error)
    Execute(er *ExecuteRequest, nodeAddr string, timeout time.Duration) error
    RemoveNode(rn *RemoveNodeRequest, nodeAddr string, timeout time.Duration) error
}
```

## Security and Authentication

### TLS Configuration

**Node-to-Node TLS:**
```go
tlsConfig := &tls.Config{
    Certificates: []tls.Certificate{cert},
    RootCAs:      caCertPool,
    ClientAuth:   tls.RequireAndVerifyClientCert,
    ClientCAs:    caCertPool,
}
```

### Authentication Mechanisms

1. **Basic Authentication**: HTTP Basic Auth
2. **Token-based**: JWT tokens (planned)
3. **Mutual TLS**: Certificate-based authentication

## Performance and Scalability

### Performance Optimizations

1. **Connection Pooling**
   - Reuse TCP connections
   - Configurable pool size
   - Health checking

2. **Batch Processing**
   - Group operations for efficiency
   - Configurable batch sizes
   - Time-based flushing

3. **Compression**
   - Gzip for snapshots
   - Optional compression for Raft logs
   - Network compression support

### Scalability Patterns

**Horizontal Scaling:**
- Add nodes to cluster
- Automatic rebalancing
- Load distribution

**Vertical Scaling:**
- Increase worker pool size
- Tune buffer sizes
- Optimize batch sizes

### Resource Management

```go
const (
    raftLogCacheSize    = 128
    connectionPoolCount = 5
    observerChanLen     = 50
    defaultWorkerCount  = runtime.NumCPU()
)
```

## Deployment Architecture

### Docker Deployment

**Container Structure:**
```dockerfile
FROM golang:1.23.1-alpine AS builder
# Build stage with dependencies

FROM alpine:latest
# Runtime with minimal footprint
WORKDIR /app
EXPOSE 4001
ENTRYPOINT ["docker-entrypoint.sh"]
```

### Kubernetes Deployment (Planned)

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: wire-cluster
spec:
  replicas: 3
  serviceName: wire
  template:
    spec:
      containers:
      - name: wire
        image: wire/wire:latest
        ports:
        - containerPort: 4001  # HTTP
        - containerPort: 4002  # Raft
```

### Configuration Management

**Environment Variables:**
- `WIRE_NODE_ID`: Unique node identifier
- `WIRE_DATA_PATH`: Data directory path
- `WIRE_HTTP_ADDR`: HTTP service address
- `WIRE_RAFT_ADDR`: Raft service address
- `WIRE_JOIN_ADDR`: Cluster join addresses

## Monitoring and Observability

### Metrics Collection

**Exposed Metrics:**
```go
var stats = expvar.NewMap("cluster")
stats.Add("num_execute_req", 0)
stats.Add("num_query_req", 0)
stats.Add("num_client_retries", 0)
```

### Logging Strategy

**Log Levels:**
- **TRACE**: Detailed execution flow
- **DEBUG**: Diagnostic information
- **INFO**: General information
- **WARN**: Warning conditions
- **ERROR**: Error conditions
- **FATAL**: Critical failures

**Structured Logging:**
```go
log.Info().
    Str("node_id", nodeID).
    Str("state", "leader").
    Dur("uptime", uptime).
    Msg("Node status")
```

### Health Checks

**Health Endpoints:**
- `/health`: Basic liveness check
- `/ready`: Readiness check (cluster joined)
- `/metrics`: Prometheus-compatible metrics

### Debugging Tools

1. **pprof Integration**: CPU and memory profiling
2. **Trace Logging**: Request tracing
3. **Debug Endpoints**: Internal state inspection

## System Constraints and Limitations

### Current Limitations

1. **Storage**: Single database backend per node
2. **Pipeline**: Single pipeline per configuration
3. **Transforms**: Limited built-in transform operations
4. **Security**: Basic authentication only

### Performance Boundaries

- **Max Cluster Size**: ~50 nodes (Raft limitation)
- **Throughput**: ~100K messages/second per node
- **Latency**: Sub-millisecond for local operations
- **Storage**: Limited by BadgerDB capacity

## Future Enhancements

### Planned Features

1. **Multi-Pipeline Support**: Multiple concurrent pipelines
2. **Dynamic Configuration**: Hot reload of pipeline configs
3. **Advanced Transforms**: ML-based transformations
4. **Federation**: Cross-cluster communication
5. **WebAssembly Plugins**: Custom transform functions

### Architecture Evolution

- Migration to gRPC for internal communication
- Support for streaming protocols (WebSocket, SSE)
- Integration with cloud-native services
- Enhanced observability with OpenTelemetry

## Conclusion

Wire's low-level design demonstrates a robust, scalable architecture for distributed stream processing. The system leverages Go's concurrency primitives, Raft consensus for reliability, and a modular design for extensibility. The combination of efficient networking, flexible pipeline processing, and strong consistency guarantees makes Wire suitable for production-grade real-time data processing workloads.

Key architectural decisions:
- **Raft consensus** ensures data consistency across nodes
- **TCP multiplexing** reduces network overhead
- **Plugin architecture** enables extensibility
- **Worker pools** provide efficient concurrent processing
- **BadgerDB** offers performant embedded storage

The system is designed to be cloud-native, containerized, and ready for modern deployment environments while maintaining simplicity and operational efficiency.
