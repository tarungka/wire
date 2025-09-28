# Wire - Comprehensive Code Documentation

## Table of Contents

1. [Overview](#1-overview)
2. [Architecture Diagrams](#2-architecture-diagrams)
3. [Module Documentation](#3-module-documentation)
   - [3.1 Main Application](#31-main-application)
   - [3.2 Store Service](#32-store-service)
   - [3.3 Pipeline Service](#33-pipeline-service)
   - [3.4 HTTP Service](#34-http-service)
   - [3.5 Cluster Service](#35-cluster-service)
   - [3.6 Network Layer](#36-network-layer)
   - [3.7 Sources and Sinks](#37-sources-and-sinks)
4. [Interface Reference](#4-interface-reference)
5. [Function Reference](#5-function-reference)
6. [Flow Diagrams](#6-flow-diagrams)
7. [Code Examples](#7-code-examples)
8. [Appendices](#8-appendices)

---

## 1. Overview

Wire is a distributed stream processing framework built in Go, featuring:
- **Raft consensus** for distributed coordination
- **Pipeline-based** data processing
- **TCP multiplexing** for efficient network communication
- **Plugin architecture** for extensible sources and sinks
- **BadgerDB/RocksDB** for persistent storage

### Code Structure

```
wire/
├── cmd/                    # Main application entry point
│   ├── main.go            # Application bootstrap
│   ├── init.go            # Configuration and flags
│   └── signals.go         # Signal handling
├── internal/
│   ├── cluster/           # Cluster coordination
│   ├── http/              # HTTP API service
│   ├── new/store/         # Raft-backed store
│   ├── pipeline/          # Data pipeline engine
│   ├── tcp/               # Network layer
│   └── ...
├── sources/               # Data source connectors
└── sinks/                 # Data sink connectors
```

---

## 2. Architecture Diagrams

### System Initialization Flow

```mermaid
sequenceDiagram
    participant Main as main()
    participant Config as Config
    participant Mux as TCP Mux
    participant Store as Store Service
    participant Cluster as Cluster Service
    participant HTTP as HTTP Service
    participant Raft as Raft

    Main->>Main: HandleSignals()
    Main->>Main: CreateContext()
    Main->>Config: initFlags()
    Config-->>Main: *Config
    Main->>Mux: startNodeMux()
    Mux-->>Main: *tcp.Mux
    Main->>Store: createStore()
    Store->>Store: New()
    Store-->>Main: *NodeStore
    Main->>Cluster: clusterService()
    Cluster-->>Main: *Service
    Main->>HTTP: startHTTPService()
    HTTP-->>Main: *Service
    Main->>Store: Open()
    Store->>Raft: NewRaft()
    Main->>Main: createCluster()
    Note over Main: Application Running
    Main->>Main: <-ctx.Done()
    Main->>HTTP: Close()
    Main->>Cluster: Close()
    Main->>Store: Close()
```

### Data Pipeline Flow

```mermaid
graph LR
    subgraph Source
        S1[Connect]
        S2[LoadInitialData]
        S3[Read]
    end

    subgraph Pipeline
        P1[Partitioner]
        P2[Transform]
        P3[Worker Pool]
    end

    subgraph Sink
        K1[Connect]
        K2[Write]
        K3[Disconnect]
    end

    S1 --> S2
    S2 --> S3
    S3 --> P1
    P1 --> P2
    P2 --> P3
    P3 --> K2
    K1 -.-> K2
    K2 --> K3
```

### TCP Multiplexing Architecture

```mermaid
graph TB
    subgraph TCP Listener
        L[Listen :4002]
    end

    subgraph Multiplexer
        M[Mux Handler]
        M1[Header 1: Raft]
        M2[Header 2: Cluster]
    end

    subgraph Services
        R[Raft Service]
        C[Cluster Service]
    end

    L --> M
    M --> M1
    M --> M2
    M1 --> R
    M2 --> C
```

### Raft State Machine

```mermaid
stateDiagram-v2
    [*] --> Follower
    Follower --> Candidate: Election timeout
    Candidate --> Leader: Majority votes
    Candidate --> Follower: Discover leader
    Leader --> Follower: Discover higher term
    Follower --> Follower: Heartbeat from leader
```

---

## 3. Module Documentation

### 3.1 Main Application

#### Package: `main`
#### Location: `cmd/`

##### Core Files

###### main.go (cmd/main.go)

**Purpose**: Application entry point and orchestration

**Key Functions**:

```go
// Function: main
// File: cmd/main.go:48
// Signature: func main()
// Description: Bootstrap the Wire application
// Flow:
//   1. Setup signal handling
//   2. Create context
//   3. Initialize configuration
//   4. Setup logging
//   5. Create network mux
//   6. Initialize store
//   7. Create cluster service
//   8. Start HTTP service
//   9. Open store
//   10. Join/bootstrap cluster
//   11. Wait for shutdown signal
//   12. Cleanup resources
```

```go
// Function: startNodeMux
// File: cmd/main.go:212
// Signature: func startNodeMux(cfg *Config, ln net.Listener) (*tcp.Mux, error)
// Description: Initialize TCP multiplexer for node communication
// Parameters:
//   - cfg: Application configuration
//   - ln: Network listener
// Returns:
//   - *tcp.Mux: Configured multiplexer
//   - error: Any initialization error
// Implementation:
//   - Creates TCP or TLS mux based on config
//   - Sets advertised address
//   - Starts mux.Serve() goroutine
```

```go
// Function: createStore
// File: cmd/main.go:277
// Signature: func createStore(cfg *Config, ly *tcp.Layer) (*store.NodeStore, error)
// Description: Create and configure the Raft-backed store
// Parameters:
//   - cfg: Application configuration
//   - ly: TCP layer for Raft communication
// Returns:
//   - *store.NodeStore: Configured store instance
//   - error: Creation error
// Configuration:
//   - Sets Raft timeouts
//   - Configures snapshot settings
//   - Initializes database backend
```

```go
// Function: createCluster
// File: cmd/main.go:350
// Signature: func createCluster(ctx context.Context, cfg *Config, hasPeers bool,
//                              client *cluster.Client, str *store.NodeStore,
//                              httpServ *httpd.Service, credStr *auth.CredentialsStore) error
// Description: Initialize or join a Raft cluster
// Parameters:
//   - ctx: Context for cancellation
//   - cfg: Configuration with join/bootstrap settings
//   - hasPeers: Whether node has existing peers
//   - client: Cluster client for communication
//   - str: Store instance
// Returns:
//   - error: Clustering error
// Logic:
//   - Single node: Bootstrap if no peers
//   - Join mode: Connect to specified addresses
//   - Bootstrap expect: Wait for minimum nodes
//   - Discovery mode: Use service discovery
```

###### init.go (cmd/init.go)

**Purpose**: Configuration management and validation

**Key Structures**:

```go
// Structure: Config
// File: cmd/init.go:61
type Config struct {
    // Paths
    DataPath    string  // Raft data directory
    ConfigPath  []string // Configuration files

    // Network
    HTTPAddr    string  // HTTP bind address
    HTTPAdv     string  // HTTP advertised address
    RaftAddr    string  // Raft bind address
    RaftAdv     string  // Raft advertised address

    // Security
    HTTPx509Cert string // HTTP TLS certificate
    HTTPx509Key  string // HTTP TLS key
    NodeX509Cert string // Node TLS certificate
    NodeX509Key  string // Node TLS key

    // Clustering
    NodeID              string        // Unique node identifier
    JoinAddrs           string        // Comma-separated join addresses
    BootstrapExpect     int          // Minimum nodes for bootstrap
    RaftNonVoter        bool         // Non-voting node flag

    // Raft Configuration
    RaftHeartbeatTimeout  time.Duration
    RaftElectionTimeout   time.Duration
    RaftSnapThreshold     uint64
    RaftSnapInterval      time.Duration

    // Performance
    WriteQueueCap     int
    WriteQueueBatchSz int
    WriteQueueTimeout time.Duration

    // Storage
    StoreDatabase string // Backend: badgerdb, rocksdb
}
```

**Key Functions**:

```go
// Function: initFlags
// File: cmd/init.go:505
// Signature: func initFlags(name, desc string, build *BuildInfo) (*Config, error)
// Description: Parse command-line flags and create configuration
// Parameters:
//   - name: Application name
//   - desc: Application description
//   - build: Build information
// Returns:
//   - *Config: Parsed configuration
//   - error: Parsing error
// Flags:
//   - Network: --http-addr, --raft-addr
//   - Cluster: --join, --bootstrap-expect
//   - Storage: --raft-dir, --store-db
//   - Security: --http-cert, --node-cert
```

```go
// Function: Validate
// File: cmd/init.go:244
// Signature: func (c *Config) Validate() error
// Description: Validate configuration consistency
// Validation:
//   - Path resolution
//   - Address format
//   - Certificate pairing
//   - Bootstrap constraints
//   - Join address conflicts
```

---

### 3.2 Store Service

#### Package: `store`
#### Location: `internal/new/store/`

##### Core Components

###### NodeStore Structure

```go
// Structure: NodeStore
// File: internal/new/store/store.go:102
type NodeStore struct {
    // State
    open         *rsync.AtomicBool
    bootstrapped bool

    // Raft Components
    raft       *raft.Raft          // Raft instance
    raftID     string              // Node ID
    raftDir    string              // Data directory
    raftTn     *NodeTransport      // Network transport
    raftStable raft.StableStore    // Persistent store
    raftLog    raft.LogStore       // Log store

    // FSM Components
    db           *badgerdb.DB       // Application database
    fsmIndex     *atomic.Uint64     // Last applied index
    fsmTerm      *atomic.Uint64     // Last applied term
    fsmUpdatedAt *rsync.AtomicTime  // Update timestamp

    // Snapshot
    snapshotStore raft.SnapshotStore
    snapshotDir   string

    // Storage Backend
    storeDb string              // Backend type
    dbStore db.DbStore          // Backend implementation

    // Cluster
    notifyingNodes map[string]*Server

    // Observability
    observerChan chan raft.Observation
    observer     *raft.Observer
    logger       zerolog.Logger
}
```

##### Store Operations

```go
// Function: Open
// File: internal/new/store/store.go:175
// Signature: func (s *NodeStore) Open() error
// Description: Initialize and open the store
// Flow:
//   1. Reset FSM state
//   2. Create network transport
//   3. Initialize snapshot store
//   4. Create storage backend
//   5. Setup log cache
//   6. Open BadgerDB
//   7. Create Raft instance
//   8. Register observer
// Side Effects:
//   - Creates Raft directories
//   - Opens database connections
//   - Starts Raft goroutines
```

```go
// Function: StoreInDatabase
// File: internal/new/store/store.go (interface implementation)
// Signature: func (s *NodeStore) StoreInDatabase(key, value string) error
// Description: Store key-value pair via Raft consensus
// Parameters:
//   - key: Storage key
//   - value: Storage value
// Returns:
//   - error: Storage error
// Flow:
//   1. Check if leader
//   2. Create command
//   3. Apply to Raft
//   4. Wait for application
```

```go
// Function: GetFromDatabase
// File: internal/new/store/store.go (interface implementation)
// Signature: func (s *NodeStore) GetFromDatabase(key string) (string, error)
// Description: Retrieve value from database
// Parameters:
//   - key: Lookup key
// Returns:
//   - string: Value
//   - error: Retrieval error
// Consistency:
//   - Reads from local FSM
//   - Eventually consistent
```

##### FSM Implementation

```go
// Function: Apply
// File: internal/new/store/store.go:255
// Signature: func (s *NodeStore) Apply(l *raft.Log) interface{}
// Description: Apply Raft log entry to FSM
// Parameters:
//   - l: Raft log entry
// Returns:
//   - interface{}: Application result
// Implementation:
//   1. Decode log entry
//   2. Apply to BadgerDB
//   3. Update FSM metadata
//   4. Return result/error
// Thread Safety:
//   - Protected by fsmMu mutex
//   - Atomic index/term updates
```

```go
// Function: Snapshot
// File: internal/new/store/store.go:277
// Signature: func (s *NodeStore) Snapshot() (raft.FSMSnapshot, error)
// Description: Create FSM snapshot
// Returns:
//   - raft.FSMSnapshot: Snapshot implementation
//   - error: Snapshot error
// Implementation:
//   - Read lock FSM
//   - Create snapshot wrapper
//   - Include failure callback
```

```go
// Function: Restore
// File: internal/new/store/store.go:299
// Signature: func (s *NodeStore) Restore(snapshot io.ReadCloser) error
// Description: Restore FSM from snapshot
// Parameters:
//   - snapshot: Snapshot reader
// Returns:
//   - error: Restore error
// Implementation:
//   - Clear existing state
//   - Read snapshot data
//   - Rebuild FSM state
```

##### Cluster Operations

```go
// Function: Bootstrap
// File: internal/new/store/store.go
// Signature: func (s *NodeStore) Bootstrap(server *Server) error
// Description: Bootstrap new single-node cluster
// Parameters:
//   - server: Initial server configuration
// Returns:
//   - error: Bootstrap error
// Preconditions:
//   - Store must be open
//   - No existing cluster
```

```go
// Function: Join
// File: internal/new/store/store.go
// Signature: func (s *NodeStore) Join(id, addr string, voter bool) error
// Description: Add node to cluster
// Parameters:
//   - id: Node ID
//   - addr: Node address
//   - voter: Voting rights
// Returns:
//   - error: Join error
// Requirements:
//   - Must be leader
//   - Node not already member
```

---

### 3.3 Pipeline Service

#### Package: `pipeline`
#### Location: `internal/pipeline/`

##### Core Structures

```go
// Structure: DataPipeline
// File: internal/pipeline/pipeline.go:36
type DataPipeline struct {
    // State
    open     atomic.Bool
    cancel   context.CancelFunc

    // Components
    Source   DataSource    // Data input
    Sink     DataSink      // Data output

    // Configuration
    key      string        // Pipeline identifier
    jobCount uint          // Worker count

    // Operations
    operations []*PipelineOps

    // Synchronization
    mu       sync.RWMutex
    counter  uint64        // Debug counter
}
```

```go
// Structure: Job
// File: internal/models/job.go:14
type Job struct {
    ID            uuid.UUID     // UUID v7 identifier
    data          any          // Payload (usually JSON)
    nodeCreatedAt time.Time    // Creation time
    nodeUpdatedAt time.Time    // Update time
    eventTime     time.Time    // Event time from data
    priority      int          // Processing priority
    mu            sync.RWMutex // Thread safety
}
```

##### Pipeline Operations

```go
// Function: Run
// File: internal/pipeline/pipeline.go:93
// Signature: func (dp *DataPipeline) Run(pctx context.Context)
// Description: Execute the data pipeline
// Parameters:
//   - pctx: Parent context for cancellation
// Flow:
//   1. Create pipeline context
//   2. Connect to source
//   3. Connect to sink
//   4. Load initial data
//   5. Start reading stream
//   6. Partition data by hash
//   7. Create worker goroutines
//   8. Process until context done
//   9. Cleanup resources
// Concurrency:
//   - Multiple worker goroutines
//   - Channel-based communication
//   - WaitGroup synchronization
```

```go
// Function: processJob
// File: internal/pipeline/pipeline.go:163
// Signature: func (dp *DataPipeline) processJob(ctx context.Context, wg *sync.WaitGroup,
//                  t *transform.Transformer, dataChannel <-chan *models.Job,
//                  initialDataChannel <-chan *models.Job)
// Description: Process jobs in a worker
// Parameters:
//   - ctx: Context for cancellation
//   - wg: WaitGroup for synchronization
//   - t: Transformer instance
//   - dataChannel: Streaming data
//   - initialDataChannel: Initial load data
// Implementation:
//   1. Apply transformations
//   2. Write to sink
//   3. Handle errors
//   4. Cleanup on exit
```

##### Transform Operations

```go
// Interface: Operation
// File: internal/pipeline/pipeline.go:19
type Operation interface {
    ID() string
    Process(ctx context.Context, in <-chan *models.Job) <-chan *models.Job
}
```

```go
// Function: toUpperCaseJSON
// File: internal/pipeline/pipeline.go:282
// Signature: func toUpperCaseJSON(ctx context.Context, in <-chan *models.Job) <-chan *models.Job
// Description: Example transform - convert JSON strings to uppercase
// Parameters:
//   - ctx: Context for cancellation
//   - in: Input job channel
// Returns:
//   - <-chan *models.Job: Transformed jobs
// Implementation:
//   - Concurrent processing
//   - Type-safe JSON handling
//   - Context-aware cancellation
```

##### Partitioning

```go
// Structure: Partitioner
// File: internal/partitioner/partition.go
type Partitioner[T any] struct {
    numPartitions int
    hashFn        func(T) uint32
}

// Function: PartitionData
// Description: Distribute data across partitions
// Algorithm:
//   1. Read from input channel
//   2. Apply hash function
//   3. Route to partition channel
//   4. Balance load across workers
```

---

### 3.4 HTTP Service

#### Package: `http`
#### Location: `internal/http/`

##### Service Structure

```go
// Structure: Service
// File: internal/http/service.go:323
type Service struct {
    // Network
    httpServer http.Server
    addr       string
    ln         net.Listener

    // Backend
    store   Store    // Raft store
    cluster Cluster  // Cluster client

    // Queuing
    stmtQueue *queue.Queue[*command.Statement]
    queueDone chan struct{}

    // Configuration
    DefaultQueueCap     int
    DefaultQueueBatchSz int
    DefaultQueueTimeout time.Duration

    // Security
    CertFile        string
    KeyFile         string
    CACertFile      string
    ClientVerify    bool
    credentialStore CredentialStore

    // Metadata
    BuildInfo map[string]interface{}
    start     time.Time

    // Synchronization
    statusMu sync.RWMutex
    statuses map[string]StatusReporter

    logger zerolog.Logger
}
```

##### API Endpoints

```go
// Handler: handleExecute
// Method: POST
// Path: /db/execute
// Description: Execute write operations
// Request Body: JSON array of statements
// Response: ExecuteQueryResponse
// Flow:
//   1. Parse request
//   2. Validate permissions
//   3. Check if leader
//   4. Apply to Raft
//   5. Return results
```

```go
// Handler: handleQuery
// Method: GET
// Path: /db/query
// Parameters:
//   - q: Query statements
//   - consistency: strong|weak
// Response: QueryRows
// Flow:
//   1. Parse query parameters
//   2. Check consistency level
//   3. Execute on store
//   4. Format response
```

```go
// Handler: handleJoin
// Method: POST
// Path: /cluster/join
// Request Body:
//   {
//     "id": "node-id",
//     "addr": "raft-address",
//     "voter": true
//   }
// Response: Success/error
// Authorization: Requires cluster permissions
```

```go
// Handler: handleStatus
// Method: GET
// Path: /status
// Response: Node and cluster status
// Information:
//   - Node state
//   - Raft statistics
//   - Store metrics
//   - Runtime information
```

##### Queue Management

```go
// Function: queuedExecute
// Description: Queue write operations for batch processing
// Implementation:
//   1. Add to queue
//   2. Wait for batch size or timeout
//   3. Execute batch
//   4. Handle failures
// Benefits:
//   - Improved throughput
//   - Reduced Raft overhead
//   - Automatic batching
```

---

### 3.5 Cluster Service

#### Package: `cluster`
#### Location: `internal/cluster/`

##### Client Implementation

```go
// Structure: Client
// File: internal/cluster/client.go:65
type Client struct {
    dialer  Dialer
    timeout time.Duration

    // Local optimization
    localMu       sync.RWMutex
    localNodeAddr string
    localServ     *Service

    // Connection pooling
    poolMu sync.RWMutex
    pools  map[string]pool.Pool

    logger zerolog.Logger
}
```

```go
// Function: Execute
// File: internal/cluster/client.go
// Signature: func (c *Client) Execute(er *command.ExecuteRequest, nodeAddr string,
//                  creds *proto.Credentials, timeout time.Duration, retries int)
//                  ([]*command.ExecuteQueryResponse, error)
// Description: Execute command on remote node
// Parameters:
//   - er: Execute request
//   - nodeAddr: Target node address
//   - creds: Authentication credentials
//   - timeout: Request timeout
//   - retries: Retry count
// Returns:
//   - Response array
//   - Error if failed
// Features:
//   - Automatic retries
//   - Connection pooling
//   - Local optimization
```

##### Service Implementation

```go
// Structure: Service
// File: internal/cluster/service.go
type Service struct {
    ln   net.Listener
    db   Database
    mgr  Manager
    addr net.Addr

    // State
    open bool
    mu   sync.Mutex

    // Configuration
    EnableHTTPS bool
    apiAddr     string

    logger zerolog.Logger
}
```

```go
// Function: handleConn
// Description: Process incoming cluster connection
// Protocol:
//   1. Read command type
//   2. Unmarshal request
//   3. Execute operation
//   4. Marshal response
//   5. Send response
// Commands:
//   - COMMAND_TYPE_GET_NODE_API
//   - COMMAND_TYPE_EXECUTE
//   - COMMAND_TYPE_QUERY
//   - COMMAND_TYPE_REMOVE_NODE
```

##### Join/Bootstrap Operations

```go
// Structure: Joiner
// File: internal/cluster/join.go
type Joiner struct {
    client      *Client
    numAttempts int
    interval    time.Duration
    credentials *proto.Credentials
}

// Function: Do
// Description: Join node to cluster
// Algorithm:
//   1. Try each join address
//   2. Send join request
//   3. Retry on failure
//   4. Return on success
```

```go
// Structure: Bootstrapper
// File: internal/cluster/bootstrap.go
type Bootstrapper struct {
    provider AddressProvider
    client   *Client
    credentials *proto.Credentials
}

// Function: Boot
// Description: Bootstrap cluster with multiple nodes
// Algorithm:
//   1. Notify all nodes
//   2. Wait for expected count
//   3. Leader initiates bootstrap
//   4. Others wait for leader
```

---

### 3.6 Network Layer

#### Package: `tcp`
#### Location: `internal/tcp/`

##### TCP Multiplexer

```go
// Structure: Mux
// File: internal/tcp/mux.go:83
type Mux struct {
    ln   net.Listener
    addr net.Addr
    m    map[byte]*listener  // Header to listener mapping

    wg      sync.WaitGroup
    Timeout time.Duration

    tlsConfig *tls.Config
    Logger    zerolog.Logger
}
```

```go
// Function: Serve
// File: internal/tcp/mux.go:153
// Signature: func (mux *Mux) Serve() error
// Description: Accept and multiplex connections
// Implementation:
//   1. Accept connection
//   2. Read first byte (header)
//   3. Route to appropriate listener
//   4. Handle in goroutine
// Concurrency:
//   - Non-blocking accept
//   - Concurrent connection handling
//   - WaitGroup tracking
```

```go
// Function: handleConn
// File: internal/tcp/mux.go
// Description: Route connection based on header
// Protocol:
//   - Byte 1 (0x01): Raft protocol
//   - Byte 2 (0x02): Cluster protocol
// Error Handling:
//   - Timeout on header read
//   - Unknown header logging
//   - Connection cleanup
```

##### Layer Abstraction

```go
// Structure: Layer
// File: internal/tcp/mux.go:40
type Layer struct {
    ln     net.Listener
    addr   net.Addr
    dialer *Dialer
    logger zerolog.Logger
}

// Function: Dial
// Description: Create outgoing connection
// Features:
//   - TLS support
//   - Timeout handling
//   - Header injection
```

##### Connection Pooling

```go
// Structure: Pool
// File: internal/tcp/pool/pool.go
type Pool interface {
    Get() (net.Conn, error)
    Put(conn net.Conn) error
    Close() error
    Len() int
}

// Implementation: channelPool
// Features:
//   - Fixed capacity
//   - Health checking
//   - Idle timeout
//   - Concurrent safe
```

---

### 3.7 Sources and Sinks

#### Common Interfaces

```go
// Interface: DataSource
// File: internal/pipeline/model.go
type DataSource interface {
    Init(args SourceConfig) error
    Connect(ctx context.Context) error
    LoadInitialData(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error)
    Read(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error)
    Disconnect() error
    Name() string
    Info() string
}
```

```go
// Interface: DataSink
// File: internal/pipeline/model.go
type DataSink interface {
    Init(args SinkConfig) error
    Connect(ctx context.Context) error
    Write(ctx context.Context, wg *sync.WaitGroup,
          dataChan <-chan *models.Job,
          initialDataChan <-chan *models.Job) error
    Disconnect() error
    Name() string
    Info() string
}
```

#### Kafka Source Implementation

```go
// Structure: KafkaSource
// File: sources/kafka.go:16
type KafkaSource struct {
    // Configuration
    bootstrapServers string
    consumerGroup    string
    topic            string

    // Runtime
    kafkaConsumerClient *kgo.Client

    // Metadata
    pipelineKey  string
    pipelineName string
}
```

```go
// Function: Read
// File: sources/kafka.go:69
// Signature: func (k *KafkaSource) Read(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error)
// Description: Consume messages from Kafka
// Implementation:
//   1. Create output channel
//   2. Start consumer goroutine
//   3. Poll for messages
//   4. Convert to Job objects
//   5. Send to channel
// Features:
//   - Auto-commit
//   - Error handling
//   - Graceful shutdown
```

#### MongoDB Source Implementation

```go
// Structure: MongoDBSource
// File: sources/mongo.go
type MongoDBSource struct {
    // Configuration
    connectionString string
    database         string
    collection       string

    // Runtime
    mongoClient     *mongo.Client
    mongoDatabase   *mongo.Database
    mongoCollection *mongo.Collection

    // Change Stream
    changeStreamOptions *options.ChangeStreamOptions
}
```

```go
// Function: Read
// Description: Watch MongoDB change stream
// Implementation:
//   1. Open change stream
//   2. Process change events
//   3. Extract full document
//   4. Create Job objects
//   5. Handle resume tokens
```

#### Kafka Sink Implementation

```go
// Structure: KafkaSink
// File: sinks/kafka.go:15
type KafkaSink struct {
    // Configuration
    bootstrapServers string
    topic            string

    // Runtime
    kafkaProducerClient *kgo.Client
}
```

```go
// Function: Write
// File: sinks/kafka.go:79
// Signature: func (k *KafkaSink) Write(ctx context.Context, wg *sync.WaitGroup,
//                  dataChan <-chan *models.Job, initialDataChan <-chan *models.Job) error
// Description: Produce messages to Kafka
// Implementation:
//   1. Start writer goroutine
//   2. Read from channels
//   3. Convert Job to bytes
//   4. Send to Kafka
//   5. Wait for acknowledgment
// Features:
//   - Async production
//   - Error callbacks
//   - Dual channel support
```

#### File Sink Implementation

```go
// Structure: FileSink
// File: sinks/file.go
type FileSink struct {
    // Configuration
    filePath   string
    bufferSize int

    // Runtime
    file   *os.File
    writer *bufio.Writer
}
```

```go
// Function: Write
// Description: Write data to file
// Features:
//   - Buffered writing
//   - JSON encoding
//   - Automatic flush
//   - File rotation support
```

---

## 4. Interface Reference

### Core Interfaces

#### Database Interface

```go
// Interface: Database
// Package: internal/http
// File: internal/http/service.go:49
type Database interface {
    // Write operation
    StoreInDatabase(key, value string) error

    // Read operation
    GetFromDatabase(key string) (string, error)

    // Load operation
    Load(lr *command.LoadRequest) error
}
```

#### Store Interface

```go
// Interface: Store
// Package: internal/http
// File: internal/http/service.go:77
type Store interface {
    Database

    // Cluster operations
    Remove(rn *command.RemoveNodeRequest) error
    LeaderAddr() (string, error)
    Ready() bool

    // Consistency operations
    Committed(timeout time.Duration) (uint64, error)

    // Monitoring
    Stats() (map[string]interface{}, error)
    Nodes() ([]raft.Server, error)
}
```

#### Cluster Interface

```go
// Interface: Cluster
// Package: internal/http
// File: internal/http/service.go:120
type Cluster interface {
    GetAddresser

    // Remote operations
    Execute(er *command.ExecuteRequest, nodeAddr string,
            creds *clstrPB.Credentials, timeout time.Duration,
            retries int) ([]*command.ExecuteQueryResponse, error)

    Request(eqr *command.ExecuteQueryRequest, nodeAddr string,
            creds *clstrPB.Credentials, timeout time.Duration,
            retries int) ([]*command.ExecuteQueryResponse, error)

    RemoveNode(rn *command.RemoveNodeRequest, nodeAddr string,
               creds *clstrPB.Credentials, timeout time.Duration) error

    Stats() (map[string]interface{}, error)
}
```

#### FSM Interface

```go
// Interface: raft.FSM
// Package: github.com/hashicorp/raft
// Implementation: internal/new/store/store.go
type FSM interface {
    // Apply log entry to state machine
    Apply(log *raft.Log) interface{}

    // Create point-in-time snapshot
    Snapshot() (FSMSnapshot, error)

    // Restore from snapshot
    Restore(io.ReadCloser) error
}
```

#### Operation Interface (Pipeline)

```go
// Interface: Operation
// Package: internal/pipeline
// File: internal/pipeline/pipeline.go:19
type Operation interface {
    // Unique identifier
    ID() string

    // Process data stream
    Process(ctx context.Context, in <-chan *models.Job) <-chan *models.Job
}
```

#### StatusReporter Interface

```go
// Interface: StatusReporter
// Package: internal/http
// File: internal/http/service.go:152
type StatusReporter interface {
    Stats() (map[string]interface{}, error)
}
```

#### CredentialStore Interface

```go
// Interface: CredentialStore
// Package: internal/http
// File: internal/http/service.go:146
type CredentialStore interface {
    // Authenticate and authorize
    AA(username, password, perm string) bool
}
```

#### Dialer Interface

```go
// Interface: Dialer
// Package: internal/cluster
// File: internal/cluster/service.go:82
type Dialer interface {
    // Create connection with timeout
    Dial(address string, timeout time.Duration) (net.Conn, error)
}
```

---

## 5. Function Reference

### Main Application Functions

```go
// Function: HandleSignals
// Package: main
// File: cmd/signals.go
// Signature: func HandleSignals(sigs ...os.Signal) chan os.Signal
// Description: Setup signal handling for graceful shutdown
// Parameters:
//   - sigs: OS signals to handle
// Returns:
//   - chan os.Signal: Signal channel
// Usage:
//   sigCh := HandleSignals(syscall.SIGINT, syscall.SIGTERM)
```

```go
// Function: CreateContext
// Package: main
// File: cmd/signals.go
// Signature: func CreateContext(sigCh chan os.Signal) (context.Context, context.CancelFunc)
// Description: Create cancellable context from signal channel
// Parameters:
//   - sigCh: Signal channel
// Returns:
//   - context.Context: Cancellable context
//   - context.CancelFunc: Cancel function
```

```go
// Function: getHostIP
// Package: main
// File: cmd/main.go:520
// Signature: func getHostIP() (string, error)
// Description: Get primary non-loopback IP address
// Returns:
//   - string: IP address
//   - error: If no IP found
// Algorithm:
//   1. Get network interfaces
//   2. Filter loopback
//   3. Return first IPv4
```

### Store Functions

```go
// Function: IsNewNode
// Package: store
// File: internal/new/store/store.go
// Signature: func IsNewNode(dir string) bool
// Description: Check if node has existing state
// Parameters:
//   - dir: Data directory
// Returns:
//   - bool: True if new node
// Checks:
//   - Raft peers file
//   - Raft log
//   - Snapshot directory
```

```go
// Function: WaitForLeader
// Package: store
// File: internal/new/store/store.go
// Signature: func (s *NodeStore) WaitForLeader(timeout time.Duration) error
// Description: Wait for cluster to elect leader
// Parameters:
//   - timeout: Maximum wait time
// Returns:
//   - error: Timeout error
// Implementation:
//   - Poll leader address
//   - Sleep between checks
//   - Return on leader found
```

```go
// Function: IsLeader
// Package: store
// File: internal/new/store/store.go
// Signature: func (s *NodeStore) IsLeader() bool
// Description: Check if current node is leader
// Returns:
//   - bool: Leadership status
// Thread Safety: Safe to call concurrently
```

### Pipeline Functions

```go
// Function: NewDataPipeline
// Package: pipeline
// File: internal/pipeline/pipeline.go:259
// Signature: func NewDataPipeline(source DataSource, sink DataSink) *DataPipeline
// Description: Create new pipeline instance
// Parameters:
//   - source: Data source
//   - sink: Data sink
// Returns:
//   - *DataPipeline: Configured pipeline
```

```go
// Function: AddOperation
// Package: pipeline
// File: internal/pipeline/pipeline.go:221
// Signature: func (dp *DataPipeline) AddOperation(op Operation) (*DataPipeline, error)
// Description: Add transform operation to pipeline
// Parameters:
//   - op: Operation to add
// Returns:
//   - *DataPipeline: Modified pipeline
//   - error: Addition error
// Thread Safety: Protected by mutex
```

### HTTP Service Functions

```go
// Function: ServeHTTP
// Package: http
// File: internal/http/service.go
// Signature: func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request)
// Description: Main HTTP request router
// Implementation:
//   1. Add CORS headers
//   2. Check authentication
//   3. Route to handler
//   4. Handle errors
```

```go
// Function: RegisterStatus
// Package: http
// File: internal/http/service.go
// Signature: func (s *Service) RegisterStatus(key string, provider StatusReporter) error
// Description: Register status provider
// Parameters:
//   - key: Status key
//   - provider: Status reporter
// Returns:
//   - error: Registration error
```

### Cluster Functions

```go
// Function: GetNodeAPIAddr
// Package: cluster
// File: internal/cluster/client.go
// Signature: func (c *Client) GetNodeAPIAddr(addr string, retries int, timeout time.Duration) (string, error)
// Description: Get HTTP API address for Raft node
// Parameters:
//   - addr: Raft address
//   - retries: Retry attempts
//   - timeout: Request timeout
// Returns:
//   - string: HTTP API address
//   - error: Lookup error
```

### Network Functions

```go
// Function: NewMux
// Package: tcp
// File: internal/tcp/mux.go:102
// Signature: func NewMux(ln net.Listener, adv net.Addr) (*Mux, error)
// Description: Create TCP multiplexer
// Parameters:
//   - ln: Network listener
//   - adv: Advertised address
// Returns:
//   - *Mux: Multiplexer instance
//   - error: Creation error
```

```go
// Function: Listen
// Package: tcp
// File: internal/tcp/mux.go
// Signature: func (mux *Mux) Listen(header byte) net.Listener
// Description: Create virtual listener for header
// Parameters:
//   - header: Protocol header byte
// Returns:
//   - net.Listener: Virtual listener
// Usage:
//   raftLn := mux.Listen(1)
//   clusterLn := mux.Listen(2)
```

---

## 6. Flow Diagrams

### Complete Startup Flow

```mermaid
flowchart TD
    Start([Application Start])

    Start --> Signals[Setup Signal Handlers]
    Signals --> Context[Create Main Context]
    Context --> Config[Parse Configuration]
    Config --> ValidateConfig{Validate Config}

    ValidateConfig -->|Invalid| Exit1[Exit with Error]
    ValidateConfig -->|Valid| Logging[Setup Logging]

    Logging --> NetListen[Create TCP Listener]
    NetListen --> Mux[Start Node Mux]

    Mux --> RaftLayer[Create Raft Layer]
    RaftLayer --> Store[Create Store]

    Store --> ClusterLn[Create Cluster Listener]
    ClusterLn --> ClusterSvc[Create Cluster Service]

    ClusterSvc --> ClusterClient[Create Cluster Client]
    ClusterClient --> HTTPSvc[Start HTTP Service]

    HTTPSvc --> OpenStore[Open Store]
    OpenStore --> CheckPeers{Has Peers?}

    CheckPeers -->|No| Bootstrap{Bootstrap Mode?}
    Bootstrap -->|Single| BootstrapSingle[Bootstrap Single Node]
    Bootstrap -->|Expect| WaitNodes[Wait for Nodes]

    CheckPeers -->|Yes| Join[Join Cluster]

    BootstrapSingle --> Running
    WaitNodes --> Running
    Join --> Running

    Running([Application Running])
    Running --> WaitSignal[Wait for Signal]
    WaitSignal --> Shutdown[Shutdown Sequence]

    Shutdown --> CloseHTTP[Close HTTP]
    CloseHTTP --> CloseCluster[Close Cluster]
    CloseCluster --> StepDown{Is Leader?}

    StepDown -->|Yes| Resign[Step Down]
    StepDown -->|No| CloseStore
    Resign --> CloseStore[Close Store]

    CloseStore --> Exit2([Application Exit])
```

### Pipeline Data Processing Flow

```mermaid
flowchart LR
    subgraph Source
        S1[MongoDB/Kafka Source]
        S2[Connect]
        S3[Read Stream]
    end

    subgraph Partitioning
        P1[Hash Function]
        P2[Route to Partition]
        P3[Channel Buffer]
    end

    subgraph Workers
        W1[Worker 1]
        W2[Worker 2]
        W3[Worker N]
    end

    subgraph Transform
        T1[Parse JSON]
        T2[Apply Transform]
        T3[Format Output]
    end

    subgraph Sink
        K1[Kafka/File Sink]
        K2[Buffer]
        K3[Write Batch]
    end

    S1 --> S2
    S2 --> S3
    S3 --> P1
    P1 --> P2
    P2 --> P3

    P3 --> W1
    P3 --> W2
    P3 --> W3

    W1 --> T1
    W2 --> T1
    W3 --> T1

    T1 --> T2
    T2 --> T3
    T3 --> K1
    K1 --> K2
    K2 --> K3
```

### Raft Consensus Flow

```mermaid
sequenceDiagram
    participant Client
    participant Leader
    participant Follower1
    participant Follower2
    participant FSM

    Client->>Leader: Write Request
    Leader->>Leader: Append to Log

    par Replication
        Leader->>Follower1: AppendEntries RPC
        and
        Leader->>Follower2: AppendEntries RPC
    end

    Follower1-->>Leader: Success
    Follower2-->>Leader: Success

    Note over Leader: Majority reached

    Leader->>FSM: Apply to FSM
    FSM-->>Leader: Result

    par Commit Notification
        Leader->>Follower1: Commit Index
        and
        Leader->>Follower2: Commit Index
    end

    Follower1->>Follower1: Apply to FSM
    Follower2->>Follower2: Apply to FSM

    Leader-->>Client: Write Confirmed
```

### HTTP Request Processing

```mermaid
flowchart TD
    Request[HTTP Request]

    Request --> CORS[Add CORS Headers]
    CORS --> Auth{Authenticated?}

    Auth -->|No| AuthFail[401 Unauthorized]
    Auth -->|Yes| Route{Route Path}

    Route -->|/db/execute| Execute[Execute Handler]
    Route -->|/db/query| Query[Query Handler]
    Route -->|/cluster/join| Join[Join Handler]
    Route -->|/status| Status[Status Handler]

    Execute --> CheckLeader1{Is Leader?}
    CheckLeader1 -->|No| Forward1[Forward to Leader]
    CheckLeader1 -->|Yes| ApplyRaft[Apply to Raft]

    Query --> CheckConsist{Consistency Level}
    CheckConsist -->|Strong| CheckLeader2{Is Leader?}
    CheckConsist -->|Weak| ReadLocal[Read from FSM]

    CheckLeader2 -->|No| Forward2[Forward to Leader]
    CheckLeader2 -->|Yes| ReadLeader[Read from Leader FSM]

    Join --> CheckLeader3{Is Leader?}
    CheckLeader3 -->|No| Forward3[Forward to Leader]
    CheckLeader3 -->|Yes| AddNode[Add to Cluster]

    Status --> GatherStats[Gather Statistics]

    Forward1 --> RemoteExec[Remote Execute]
    Forward2 --> RemoteQuery[Remote Query]
    Forward3 --> RemoteJoin[Remote Join]

    ApplyRaft --> WaitCommit[Wait for Commit]
    WaitCommit --> Response1[Response]

    ReadLocal --> Response2[Response]
    ReadLeader --> Response3[Response]

    AddNode --> Response4[Response]
    GatherStats --> Response5[Response]

    RemoteExec --> Response6[Response]
    RemoteQuery --> Response7[Response]
    RemoteJoin --> Response8[Response]
```

### Cluster Join Process

```mermaid
sequenceDiagram
    participant NewNode
    participant JoinAddr
    participant Leader
    participant Raft

    NewNode->>JoinAddr: Join Request

    alt JoinAddr is Leader
        JoinAddr->>Raft: AddVoter/AddNonvoter
        Raft-->>JoinAddr: Success
        JoinAddr-->>NewNode: Join Successful
    else JoinAddr is Follower
        JoinAddr->>Leader: Forward Request
        Leader->>Raft: AddVoter/AddNonvoter
        Raft-->>Leader: Success
        Leader-->>JoinAddr: Success
        JoinAddr-->>NewNode: Join Successful
    end

    NewNode->>Leader: Start Replication
    Leader->>NewNode: Send Snapshot
    NewNode->>NewNode: Restore Snapshot

    loop Replication
        Leader->>NewNode: AppendEntries
        NewNode-->>Leader: Success
    end
```

### TCP Multiplexing Flow

```mermaid
flowchart TD
    Conn[Incoming Connection]

    Conn --> Accept[Accept Connection]
    Accept --> ReadHeader[Read First Byte]

    ReadHeader --> Timeout{Timeout?}
    Timeout -->|Yes| Close1[Close Connection]
    Timeout -->|No| CheckHeader{Header Value}

    CheckHeader -->|0x01| RaftListener[Raft Listener]
    CheckHeader -->|0x02| ClusterListener[Cluster Listener]
    CheckHeader -->|Unknown| LogError[Log Unknown Header]

    RaftListener --> RaftHandler[Raft Handler]
    ClusterListener --> ClusterHandler[Cluster Handler]
    LogError --> Close2[Close Connection]

    RaftHandler --> Process1[Process Raft Protocol]
    ClusterHandler --> Process2[Process Cluster Protocol]

    Process1 --> Done1[Connection Complete]
    Process2 --> Done2[Connection Complete]
```

### Store FSM Operations

```mermaid
stateDiagram-v2
    [*] --> Closed

    Closed --> Opening: Open()
    Opening --> Open: Raft Started

    Open --> Leader: Election Won
    Open --> Follower: Join Cluster

    Leader --> Processing: Apply Command
    Follower --> Processing: Apply Command

    Processing --> Leader: Command Applied
    Processing --> Follower: Command Applied

    Leader --> Snapshotting: Snapshot Triggered
    Follower --> Snapshotting: Snapshot Triggered

    Snapshotting --> Leader: Snapshot Complete
    Snapshotting --> Follower: Snapshot Complete

    Leader --> Follower: Lost Leadership
    Follower --> Leader: Won Election

    Leader --> Closing: Close()
    Follower --> Closing: Close()

    Closing --> Closed: Resources Released
    Closed --> [*]
```

---

## 7. Code Examples

### Example: Creating and Running a Pipeline

```go
// Create source configuration
sourceConfig := SourceConfig{
    Key:            "pipeline-1",
    Name:           "kafka-source",
    ConnectionType: "kafka",
    Config: map[string]string{
        "bootstrap_servers": "localhost:9092",
        "group":            "wire-consumer",
        "topic":            "input-events",
    },
}

// Create sink configuration
sinkConfig := SinkConfig{
    Key:            "pipeline-1",
    Name:           "kafka-sink",
    ConnectionType: "kafka",
    Config: map[string]string{
        "bootstrap_servers": "localhost:9092",
        "topic":            "output-events",
    },
}

// Initialize source and sink
source := &KafkaSource{}
source.Init(sourceConfig)

sink := &KafkaSink{}
sink.Init(sinkConfig)

// Create pipeline
pipeline := NewDataPipeline(source, sink)

// Add transform operation
transformer := &UpperCaseTransform{}
pipeline.AddOperation(transformer)

// Run pipeline
ctx := context.Background()
go pipeline.Run(ctx)

// Wait for shutdown signal
<-shutdownSignal
pipeline.Close()
```

### Example: Implementing a Custom Transform

```go
type CustomTransform struct {
    id string
}

func (t *CustomTransform) ID() string {
    return t.id
}

func (t *CustomTransform) Process(ctx context.Context, in <-chan *models.Job) <-chan *models.Job {
    out := make(chan *models.Job)

    go func() {
        defer close(out)

        for {
            select {
            case <-ctx.Done():
                return

            case job, ok := <-in:
                if !ok {
                    return
                }

                // Get job data
                data, err := job.GetData()
                if err != nil {
                    continue
                }

                // Transform data
                transformed := t.transform(data)

                // Update job
                job.SetData(transformed)
                job.SetUpdatedAt(time.Now())

                // Send to output
                select {
                case out <- job:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()

    return out
}

func (t *CustomTransform) transform(data any) any {
    // Implement transformation logic
    return data
}
```

### Example: Making Cluster Client Requests

```go
// Create cluster client
dialer := tcp.NewDialer(cluster.MuxClusterHeader, nil)
client := cluster.NewClient(dialer, 10*time.Second)

// Set local node
client.SetLocal("localhost:4002", clusterService)

// Execute command on remote node
executeReq := &command.ExecuteRequest{
    Request: &command.Request{
        Statements: []*command.Statement{
            {
                Sql: "INSERT INTO data VALUES (?, ?)",
                Parameters: []*command.Parameter{
                    {Value: &command.Parameter_S{S: "key1"}},
                    {Value: &command.Parameter_S{S: "value1"}},
                },
            },
        },
    },
}

// Send to remote node
responses, err := client.Execute(
    executeReq,
    "node2:4002",
    nil,  // credentials
    5*time.Second,
    3,    // retries
)

if err != nil {
    log.Error().Err(err).Msg("Execute failed")
}
```

### Example: Custom Source Implementation

```go
type CustomSource struct {
    config map[string]string
    client CustomClient
}

func (s *CustomSource) Init(args SourceConfig) error {
    s.config = args.Config
    return s.validate()
}

func (s *CustomSource) Connect(ctx context.Context) error {
    client, err := NewCustomClient(s.config)
    if err != nil {
        return err
    }
    s.client = client
    return nil
}

func (s *CustomSource) Read(ctx context.Context, wg *sync.WaitGroup) (<-chan *models.Job, error) {
    outputChan := make(chan *models.Job, 100)

    wg.Add(1)
    go func() {
        defer wg.Done()
        defer close(outputChan)

        for {
            select {
            case <-ctx.Done():
                return

            default:
                data, err := s.client.Fetch()
                if err != nil {
                    log.Error().Err(err).Msg("Fetch failed")
                    continue
                }

                job, err := models.New(data)
                if err != nil {
                    continue
                }

                select {
                case outputChan <- job:
                case <-ctx.Done():
                    return
                }
            }
        }
    }()

    return outputChan, nil
}

func (s *CustomSource) Disconnect() error {
    if s.client != nil {
        return s.client.Close()
    }
    return nil
}
```

### Example: HTTP Handler Implementation

```go
func (s *Service) handleCustomEndpoint(w http.ResponseWriter, r *http.Request) {
    // Start timing
    start := time.Now()

    // Check authentication
    if !s.authenticate(r) {
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    // Parse request
    var req CustomRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Check if leader
    if !s.store.IsLeader() {
        leaderAddr, err := s.store.LeaderAddr()
        if err != nil {
            http.Error(w, "No leader", http.StatusServiceUnavailable)
            return
        }

        // Forward to leader
        s.forwardToLeader(w, r, leaderAddr)
        return
    }

    // Process request
    result, err := s.processCustomRequest(&req)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Build response
    resp := &Response{
        Results: result,
        Time:    time.Since(start).Seconds(),
    }

    // Send response
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

---

## 8. Appendices

### Appendix A: Error Codes

| Error | Code | Description |
|-------|------|-------------|
| ErrStoreNotOpen | - | Store not initialized |
| ErrNotLeader | - | Operation requires leadership |
| ErrNotReady | - | Store not ready for requests |
| ErrStaleRead | - | Read would violate consistency |
| ErrOpenTimeout | - | Store open timeout |
| ErrLoadInProgress | - | Concurrent load detected |
| ErrNotImplemented | - | Feature not implemented |

### Appendix B: Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| --http-addr | localhost:4001 | HTTP API bind address |
| --raft-addr | localhost:4002 | Raft bind address |
| --node-id | (raft-addr) | Unique node identifier |
| --join | - | Addresses to join |
| --bootstrap-expect | 0 | Nodes for bootstrap |
| --raft-timeout | 1s | Raft heartbeat timeout |
| --raft-snap | 8192 | Snapshot threshold |
| --store-db | bbolt | Storage backend |

### Appendix C: Protocol Headers

| Header | Value | Protocol |
|--------|-------|----------|
| MuxRaftHeader | 0x01 | Raft consensus |
| MuxClusterHeader | 0x02 | Cluster service |

### Appendix D: File Structure Reference

```
internal/
├── cluster/
│   ├── bootstrap.go      # Cluster bootstrap
│   ├── client.go         # Cluster client
│   ├── join.go          # Join operations
│   ├── remove.go        # Remove operations
│   └── service.go       # Cluster service
├── command/
│   ├── encoding/        # Command encoding
│   ├── proto/          # Protocol buffers
│   └── marshal.go      # Marshaling
├── http/
│   ├── connector.go    # HTTP connectors
│   ├── nodes.go       # Node endpoints
│   ├── service.go     # HTTP service
│   └── util.go        # Utilities
├── new/
│   ├── db/           # Database backends
│   └── store/        # Store implementation
├── pipeline/
│   ├── config.go     # Configuration
│   ├── model.go      # Data models
│   ├── ops.go        # Operations
│   ├── pipeline.go   # Pipeline engine
│   └── worker.go     # Worker pool
└── tcp/
    ├── dialer.go     # TCP dialer
    ├── mux.go        # Multiplexer
    ├── network.go    # Network utilities
    └── pool/         # Connection pooling
```

### Appendix E: Performance Metrics

| Metric | Value | Notes |
|--------|-------|-------|
| Throughput | ~100K msg/s | Per node |
| Latency | <1ms | Local operations |
| Cluster Size | 3-7 nodes | Recommended |
| Max Cluster | ~50 nodes | Raft limitation |
| Snapshot Size | Variable | Depends on data |
| Recovery Time | <30s | Leader election |

### Appendix F: Monitoring Points

1. **Store Metrics**
   - `fsmIndex`: Last applied index
   - `fsmTerm`: Last applied term
   - `leaderAddr`: Current leader
   - `nodeState`: Raft state

2. **HTTP Metrics**
   - `numExecutions`: Execute count
   - `numQueries`: Query count
   - `numRemoteExecutions`: Forwarded executes
   - `numAuthFail`: Auth failures

3. **Cluster Metrics**
   - `numConnections`: Active connections
   - `numRetries`: Client retries
   - `poolSize`: Connection pool size

4. **Pipeline Metrics**
   - `jobsProcessed`: Total jobs
   - `transformTime`: Transform duration
   - `sinkWriteTime`: Write latency
   - `errorCount`: Processing errors

---

## Conclusion

This comprehensive code documentation provides a complete reference for the Wire distributed stream processing framework. The documentation covers:

1. **Complete code structure** with file locations and line numbers
2. **All major functions** with signatures and descriptions
3. **Interface definitions** and their implementations
4. **Detailed flow diagrams** using Mermaid
5. **Practical code examples** for common use cases
6. **Configuration reference** and monitoring points

The Wire system demonstrates sophisticated engineering patterns including:
- Raft consensus for distributed coordination
- Channel-based concurrent processing
- TCP multiplexing for efficient networking
- Plugin architecture for extensibility
- Careful resource management and cleanup

This documentation serves as a complete technical reference for developers working with or extending the Wire system.