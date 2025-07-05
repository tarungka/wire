# Wire Architecture Diagrams

## 1. High-Level System Architecture

```mermaid
graph TB
    subgraph "External Systems"
        MongoDB[(MongoDB)]
        Kafka1[Kafka Source]
        ES[(Elasticsearch)]
        Kafka2[Kafka Sink]
        FileSystem[(File System)]
    end

    subgraph "Wire Cluster"
        subgraph "Node 1 (Leader)"
            HTTP1[HTTP API Service<br/>:8080]
            Pipeline1[Pipeline Engine]
            Raft1[Raft Leader]
            Store1[(BadgerDB)]
            Transport1[TCP Mux<br/>:8089]
        end

        subgraph "Node 2 (Follower)"
            HTTP2[HTTP API Service<br/>:8081]
            Pipeline2[Pipeline Engine]
            Raft2[Raft Follower]
            Store2[(BadgerDB)]
            Transport2[TCP Mux<br/>:8090]
        end

        subgraph "Node 3 (Follower)"
            HTTP3[HTTP API Service<br/>:8082]
            Pipeline3[Pipeline Engine]
            Raft3[Raft Follower]
            Store3[(BadgerDB)]
            Transport3[TCP Mux<br/>:8091]
        end
    end

    MongoDB -->|Change Streams| Pipeline1
    Kafka1 -->|Consumer| Pipeline1
    
    Pipeline1 -->|Processed Data| ES
    Pipeline1 -->|Processed Data| Kafka2
    Pipeline1 -->|Processed Data| FileSystem

    HTTP1 <-->|API Requests| Client[Client Applications]
    
    Raft1 <-->|Consensus| Raft2
    Raft1 <-->|Consensus| Raft3
    Raft2 <-->|Consensus| Raft3

    Transport1 <-->|RPC| Transport2
    Transport1 <-->|RPC| Transport3
    Transport2 <-->|RPC| Transport3

    style Raft1 fill:#f96,stroke:#333,stroke-width:4px
    style HTTP1 fill:#9f9,stroke:#333,stroke-width:2px
```

## 2. Internal Component Architecture

```mermaid
graph LR
    subgraph "Wire Node Internal Architecture"
        subgraph "Entry Points"
            Main[main.go]
            Init[init.go]
            Signals[signals.go]
        end

        subgraph "Core Services"
            HTTPService[HTTP Service<br/>gin/chi router]
            TCPMux[TCP Multiplexer<br/>Port sharing]
            ClusterMgmt[Cluster Manager]
        end

        subgraph "Distributed Store"
            RaftEngine[Raft Consensus<br/>Engine]
            FSM[Finite State<br/>Machine]
            BadgerDB[(BadgerDB<br/>Key-Value Store)]
            LogStore[(Raft Log Store<br/>Badger/Bolt/Rocks)]
            StableStore[(Stable Store<br/>Config/State)]
        end

        subgraph "Pipeline System"
            PipelineEngine[Pipeline<br/>Orchestrator]
            JobProcessor[Job Processor<br/>Partitioned]
            TransformOps[Transform<br/>Operations]
            Sources[Source<br/>Adapters]
            Sinks[Sink<br/>Adapters]
        end

        Main --> Init
        Init --> HTTPService
        Init --> TCPMux
        Init --> RaftEngine

        HTTPService --> ClusterMgmt
        HTTPService --> PipelineEngine
        HTTPService --> FSM

        TCPMux --> RaftEngine

        RaftEngine --> FSM
        FSM --> BadgerDB
        RaftEngine --> LogStore
        RaftEngine --> StableStore

        PipelineEngine --> Sources
        PipelineEngine --> TransformOps
        TransformOps --> JobProcessor
        JobProcessor --> Sinks

        ClusterMgmt --> RaftEngine
    end
```

## 3. Pipeline Processing Flow

```mermaid
graph TD
    subgraph "Data Pipeline Processing"
        Source[Data Source<br/>MongoDB/Kafka]
        
        subgraph "Pipeline Engine"
            Queue[Job Queue<br/>Channel Buffer]
            
            subgraph "Parallel Processing"
                Worker1[Worker 1<br/>Goroutine]
                Worker2[Worker 2<br/>Goroutine]
                Worker3[Worker N<br/>Goroutine]
            end
            
            Transform[Transform<br/>Operations]
            Partitioner[Job<br/>Partitioner]
        end
        
        subgraph "Sinks"
            SinkRouter[Sink Router]
            ESSink[Elasticsearch<br/>Sink]
            KafkaSink[Kafka<br/>Sink]
            FileSink[File<br/>Sink]
        end
        
        Source -->|Raw Data| Queue
        Queue --> Partitioner
        Partitioner --> Worker1
        Partitioner --> Worker2
        Partitioner --> Worker3
        
        Worker1 --> Transform
        Worker2 --> Transform
        Worker3 --> Transform
        
        Transform --> SinkRouter
        SinkRouter --> ESSink
        SinkRouter --> KafkaSink
        SinkRouter --> FileSink
        
        Transform -.->|State Updates| RaftStore[Raft Store]
    end
```

## 4. Raft Consensus Implementation

```mermaid
sequenceDiagram
    participant Client
    participant Leader
    participant Follower1
    participant Follower2
    participant FSM
    participant BadgerDB

    Client->>Leader: HTTP Write Request
    Leader->>Leader: Create Log Entry
    
    par Replicate to Followers
        Leader->>Follower1: AppendEntries RPC
        Leader->>Follower2: AppendEntries RPC
    and
        Follower1->>Follower1: Write to Log
        Follower2->>Follower2: Write to Log
    end
    
    Follower1-->>Leader: Success
    Follower2-->>Leader: Success
    
    Leader->>Leader: Commit Entry
    Leader->>FSM: Apply(log)
    FSM->>BadgerDB: Store Key-Value
    BadgerDB-->>FSM: Success
    FSM-->>Leader: Result
    
    par Commit Notification
        Leader->>Follower1: Commit Index
        Leader->>Follower2: Commit Index
    and
        Follower1->>FSM: Apply(log)
        Follower2->>FSM: Apply(log)
    end
    
    Leader-->>Client: HTTP Response
```

## 5. Store Layer Architecture

```mermaid
classDiagram
    class NodeStore {
        -raft: *raft.Raft
        -db: *badger.DB
        -config: *Config
        -mu: sync.RWMutex
        +Bootstrap(servers)
        +Join(nodeID, httpAddr, raftAddr)
        +StoreInDatabase(key, value)
        +GetFromDatabase(key)
        +Apply(log) interface{}
        +Snapshot() FSMSnapshot
        +Restore(io.ReadCloser)
    }

    class FSM {
        <<interface>>
        +Apply(log) interface{}
        +Snapshot() FSMSnapshot
        +Restore(io.ReadCloser)
    }

    class StableStore {
        <<interface>>
        +Set(key, val []byte)
        +Get(key []byte) []byte
        +SetUint64(key []byte, val uint64)
        +GetUint64(key []byte) uint64
    }

    class LogStore {
        <<interface>>
        +FirstIndex() uint64
        +LastIndex() uint64
        +GetLog(index, log)
        +StoreLog(log)
        +StoreLogs(logs)
        +DeleteRange(min, max)
    }

    class BadgerDB {
        +Update(fn)
        +View(fn)
        +NewTransaction(update)
        +Close()
    }

    class Transport {
        -localAddr: net.Addr
        -consumeCh: chan RPC
        -logger: *log.Logger
        +Consumer() chan RPC
        +LocalAddr() net.Addr
        +AppendEntriesPipeline(id, target)
        +AppendEntries(id, target, args, resp)
    }

    NodeStore ..|> FSM : implements
    NodeStore ..|> StableStore : implements
    NodeStore ..|> LogStore : implements
    NodeStore --> BadgerDB : uses
    NodeStore --> Transport : uses
```

## 6. HTTP API Request Flow

```mermaid
flowchart TD
    Client[HTTP Client] --> Router{Gin Router}
    
    Router --> Status[/status]
    Router --> Join[/join]
    Router --> Key[/key/*]
    Router --> Connector[/connector/*]
    Router --> Ready[/readyz]
    
    Status --> CheckLeader1{Is Leader?}
    CheckLeader1 -->|Yes| ReturnStatus[Return Status]
    CheckLeader1 -->|No| GetLeaderStatus[Get Leader Status]
    
    Join --> CheckLeader2{Is Leader?}
    CheckLeader2 -->|Yes| ExecuteJoin[Execute Join]
    CheckLeader2 -->|No| ForwardToLeader1[Forward to Leader]
    
    Key --> ParseOp{GET/POST/DELETE?}
    ParseOp -->|GET| ReadDB[Read from DB]
    ParseOp -->|POST| CheckLeader3{Is Leader?}
    ParseOp -->|DELETE| CheckLeader3
    
    CheckLeader3 -->|Yes| WriteQueue[Queue Write Op]
    CheckLeader3 -->|No| ForwardToLeader2[Forward to Leader]
    
    WriteQueue --> ProcessQueue[Process Queue]
    ProcessQueue --> RaftApply[Raft Apply]
    RaftApply --> FSMApply[FSM Apply]
    FSMApply --> BadgerWrite[Badger Write]
    
    Connector --> PipelineOp{Operation?}
    PipelineOp --> ManagePipeline[Manage Pipeline]
```

## 7. Job Processing Architecture

```mermaid
graph TB
    subgraph "Job Model"
        Job[Job Structure]
        JobID[ID: UUID v7]
        JobData[Data: interface{}]
        JobMeta[Metadata]
        JobTime[EventTime: time.Time]
        
        Job --> JobID
        Job --> JobData
        Job --> JobMeta
        Job --> JobTime
    end
    
    subgraph "Processing Pipeline"
        Source[Source Adapter] -->|Create Jobs| JobQueue[Job Queue<br/>Buffered Channel]
        
        JobQueue --> Processor[Job Processor]
        
        Processor --> Partition1[Partition 1]
        Processor --> Partition2[Partition 2]
        Processor --> PartitionN[Partition N]
        
        Partition1 --> Transform1[Transform]
        Partition2 --> Transform2[Transform]
        PartitionN --> TransformN[Transform]
        
        Transform1 --> SinkWriter[Sink Writer]
        Transform2 --> SinkWriter
        TransformN --> SinkWriter
    end
    
    subgraph "Concurrency Control"
        WaitGroup[sync.WaitGroup]
        Context[context.Context]
        Mutex[sync.RWMutex]
        
        Processor -.-> WaitGroup
        Processor -.-> Context
        Job -.-> Mutex
    end
```

## 8. TCP Multiplexing Architecture

```mermaid
graph LR
    subgraph "TCP Multiplexer"
        Listener[TCP Listener<br/>:8089]
        
        Listener --> Acceptor[Connection<br/>Acceptor]
        
        Acceptor --> HeaderRead[Read Header<br/>1 byte]
        
        HeaderRead --> TypeSwitch{Connection Type}
        
        TypeSwitch -->|0x01| RaftRPC[Raft RPC<br/>Handler]
        TypeSwitch -->|0x02| ClusterRPC[Cluster RPC<br/>Handler]
        TypeSwitch -->|0x03| Snapshot[Snapshot<br/>Handler]
        
        RaftRPC --> RaftTransport[Raft Transport]
        ClusterRPC --> ClusterService[Cluster Service]
        Snapshot --> SnapshotTransport[Snapshot Transport]
    end
    
    Node1[Node 1] -->|TCP| Listener
    Node2[Node 2] -->|TCP| Listener
    Node3[Node 3] -->|TCP| Listener
```

## 9. Configuration and Initialization Flow

```mermaid
flowchart TD
    Start([Start]) --> ParseFlags[Parse CLI Flags]
    ParseFlags --> LoadEnv[Load Environment Vars]
    LoadEnv --> MergeConfig[Merge Configurations]
    
    MergeConfig --> Validate{Validate Config}
    Validate -->|Invalid| Exit1[Exit with Error]
    Validate -->|Valid| InitLogging[Initialize Logging]
    
    InitLogging --> CheckMode{Bootstrap Mode?}
    
    CheckMode -->|Yes| CheckSingle{Single Node?}
    CheckMode -->|No| CheckJoin{Join Addresses?}
    
    CheckSingle -->|No| Exit2[Exit: Multi-node bootstrap]
    CheckSingle -->|Yes| CreateMux[Create TCP Mux]
    
    CheckJoin -->|No| Exit3[Exit: No join addresses]
    CheckJoin -->|Yes| CreateMux
    
    CreateMux --> InitStore[Initialize Store]
    InitStore --> InitRaft[Initialize Raft]
    
    InitRaft --> BootstrapOrJoin{Bootstrap or Join?}
    
    BootstrapOrJoin -->|Bootstrap| CreateCluster[Create New Cluster]
    BootstrapOrJoin -->|Join| JoinCluster[Join Existing Cluster]
    
    CreateCluster --> StartHTTP[Start HTTP Service]
    JoinCluster --> StartHTTP
    
    StartHTTP --> LoadPipeline{Pipeline Config?}
    
    LoadPipeline -->|Yes| InitPipeline[Initialize Pipeline]
    LoadPipeline -->|No| WaitSignal[Wait for Signal]
    
    InitPipeline --> StartPipeline[Start Pipeline]
    StartPipeline --> WaitSignal
    
    WaitSignal --> Shutdown[Graceful Shutdown]
    Shutdown --> End([End])
```

## 10. State Machine Transitions

```mermaid
stateDiagram-v2
    [*] --> Initialized: Start Node
    
    Initialized --> Follower: Join Cluster
    Initialized --> Leader: Bootstrap
    
    Follower --> Candidate: Election Timeout
    Candidate --> Leader: Win Election
    Candidate --> Follower: Lose Election
    
    Leader --> Follower: Higher Term
    
    state Leader {
        [*] --> AcceptingWrites
        AcceptingWrites --> ProcessingPipeline: Pipeline Active
        ProcessingPipeline --> AcceptingWrites: Pipeline Idle
    }
    
    state Follower {
        [*] --> Replicating
        Replicating --> ApplyingLogs: Commit Index Update
        ApplyingLogs --> Replicating: Applied
    }
    
    Follower --> Shutdown: Signal
    Leader --> Shutdown: Signal
    Candidate --> Shutdown: Signal
    
    Shutdown --> [*]
```

## Summary

These diagrams illustrate the Wire distributed stream processing framework's architecture:

1. **System Architecture**: Shows the distributed nature with leader/follower nodes
2. **Component Architecture**: Internal structure of each node
3. **Pipeline Flow**: How data flows through the processing pipeline
4. **Raft Consensus**: The consensus protocol implementation
5. **Store Layer**: Class structure of the storage system
6. **HTTP API Flow**: Request routing and processing
7. **Job Processing**: Parallel job execution model
8. **TCP Multiplexing**: Network protocol handling
9. **Initialization**: Startup and configuration flow
10. **State Machine**: Raft state transitions

The architecture demonstrates a robust, scalable system designed for high-throughput stream processing with strong consistency guarantees through Raft consensus.