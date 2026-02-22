# Wire Architecture Diagrams

**Status:** Canon
**Version:** 1.0.0
**Context:** Visual reference for Wire's architecture, execution model, and operational flows

---

## 1. High-Level Cluster Architecture

The Master-Worker topology showing the Control Plane (Coordinator) and Data Plane (Workers).

```mermaid
graph TD
    subgraph Control Plane
        JM[Coordinator / Job Manager]
    end

    subgraph Data Plane
        TM1[Worker 1]
        TM2[Worker 2]
        TM3[Worker N]
    end

    subgraph External Systems
        S3[(S3 / MinIO<br/>Checkpoint Storage)]
        PROM[Prometheus<br/>Monitoring]
    end

    Client[Wire Client] -->|SubmitJob RPC| JM
    JM -->|Deploy Task DAGs| TM1
    JM -->|Deploy Task DAGs| TM2
    JM -->|Deploy Task DAGs| TM3

    TM1 -.->|Heartbeats & Metrics| JM
    TM2 -.->|Heartbeats & Metrics| JM
    TM3 -.->|Heartbeats & Metrics| JM

    TM1 <-->|Yamux TCP Mux :4001| TM2
    TM2 <-->|Yamux TCP Mux :4001| TM3
    TM1 <-->|Yamux TCP Mux :4001| TM3

    TM1 -->|Async Checkpoint Upload| S3
    TM2 -->|Async Checkpoint Upload| S3
    TM3 -->|Async Checkpoint Upload| S3

    TM1 -.->|/metrics| PROM
    TM2 -.->|/metrics| PROM
    TM3 -.->|/metrics| PROM
```

---

## 2. Execution Graph Transformation Pipeline

How user code transforms through three stages: Logical → Optimized → Physical.

```mermaid
flowchart LR
    subgraph SG["Stage 1: StreamGraph"]
        direction LR
        S[Source] --> M[Map]
        M --> W[Window / Reduce]
        W --> K[Sink]
    end

    subgraph JG["Stage 2: JobGraph"]
        direction LR
        SC["Source + Map<br/>Chained"] --> SH{Shuffle<br/>KeyBy}
        SH --> WC["Window + Sink<br/>Chained"]
    end

    subgraph EG["Stage 3: ExecutionGraph"]
        direction LR
        S1["Source+Map Task 1"] -->|Network Shuffle| W1["Window+Sink Task 1"]
        S2["Source+Map Task 2"] -->|Network Shuffle| W1
        S1 -->|Network Shuffle| W2["Window+Sink Task 2"]
        S2 -->|Network Shuffle| W2
    end

    SG -.->|"Operator Chaining & Partitioning"| JG
    JG -.->|"Parallelism Expansion (parallelism=2)"| EG
```

---

## 3. Data Flow Through a Pipeline

An example pipeline showing events, watermarks, and barriers flowing through operators.

```mermaid
flowchart LR
    subgraph Sources
        KA[Kafka Partition 0]
        KB[Kafka Partition 1]
    end

    subgraph Operators
        M1[Map Task 1]
        M2[Map Task 2]
        KBY{KeyBy<br/>Hash Partition}
        WA1[Window Agg<br/>Key Group 0-127]
        WA2[Window Agg<br/>Key Group 128-255]
    end

    subgraph Sinks
        PG1[Postgres Sink 1]
        PG2[Postgres Sink 2]
    end

    KA -->|Events + Watermarks + Barriers| M1
    KB -->|Events + Watermarks + Barriers| M2
    M1 --> KBY
    M2 --> KBY
    KBY -->|Key Group 0-127| WA1
    KBY -->|Key Group 128-255| WA2
    WA1 --> PG1
    WA2 --> PG2
```

---

## 4. Distributed Checkpointing & Barrier Alignment

The Asynchronous Barrier Snapshot (ABS) algorithm — a Chandy-Lamport variant.

```mermaid
sequenceDiagram
    participant C as Coordinator
    participant S as Source Tasks
    participant O as Downstream Operators
    participant P as Pebble State
    participant S3 as S3 / MinIO

    C->>S: TriggerCheckpoint(N)
    activate S
    S->>P: Snapshot Local State (N)
    S->>O: Forward Barrier(N) in data stream
    deactivate S

    activate O
    Note over O: Barrier Alignment:<br/>1. Barrier(N) arrives on Input A<br/>2. Buffer Input A (Epoch N+1 records)<br/>3. Continue processing Input B<br/>4. Barrier(N) arrives on Input B<br/>5. ALL inputs aligned → Snapshot
    O->>P: Snapshot Local State (N)
    O->>O: Forward Barrier(N) downstream
    deactivate O

    P->>P: Hard-link SSTables (ms)
    P-->>S3: Async upload checkpoint data

    O->>C: AcknowledgeCheckpoint(N)
    S->>C: AcknowledgeCheckpoint(N)
    C->>C: All ACKs received → Checkpoint N Complete
```

---

## 5. Fault-Tolerance & Recovery Flow

The full recovery sequence when a Worker fails.

```mermaid
sequenceDiagram
    participant TM as Worker (Failed)
    participant C as Coordinator
    participant S3 as S3 / MinIO
    participant NW as Worker (Replacement)

    Note over TM: Process crashes /<br/>Network partition
    C->>TM: Expect Heartbeat
    TM--xC: Timeout (no response)
    C->>C: Detect failure → Job = FAILING

    C->>C: Cancel ALL running tasks
    C->>S3: Fetch latest Completed Checkpoint(N) metadata

    C->>NW: Deploy tasks (Recovery Mode)
    activate NW
    NW->>S3: Download state shards for Checkpoint(N)
    S3-->>NW: SSTable files restored into Pebble
    NW->>C: Ready to process
    deactivate NW

    C->>NW: Resume from Epoch N+1
    Note over NW: Sources rewind offsets<br/>to Checkpoint(N) positions
    NW->>NW: Processing resumes
```

---

## 6. Task Slot Internal Architecture

What lives inside a single Task Slot on a Worker node.

```mermaid
flowchart LR
    subgraph WN["Worker Node"]
        subgraph TS["Task Slot"]
            direction LR
            IN["Yamux Input<br/>Stream"] --> CH1>"Input Buffer<br/>chan bytes"]
            CH1 --> DESER["Deserializer<br/>Goroutine"]
            DESER --> CH2>"Event Queue<br/>chan Record"]
            CH2 --> OP["Operator Chain<br/>Map - Filter - Window"]
            OP --> CH3>"Output Buffer<br/>chan Record"]
            CH3 --> SER["Serializer<br/>Goroutine"]
            SER --> CH4>"Network Queue<br/>chan bytes"]
            CH4 --> OUT["Yamux Output<br/>Stream"]

            OP <--> PEB[("Pebble DB<br/>per Task Slot")]
        end

        subgraph CP["Checkpoint Path"]
            direction TB
            RPC["Coordinator RPC:<br/>TriggerCheckpoint"] --> BAR["Barrier injected<br/>into stream"]
            BAR --> SNAP["Pebble Checkpoint<br/>hard-link snapshot"]
            SNAP --> UPL["Background Goroutine:<br/>Upload SSTables to S3"]
            UPL --> ACK["AcknowledgeCheckpoint<br/>to Coordinator"]
        end
    end
```

---

## 7. Backpressure Cascade

How backpressure propagates backwards through the entire pipeline — no unbounded buffering, no silent data drops.

```mermaid
flowchart RL
    KAFKA["Kafka Source<br/>stops fetching"] -->|blocked| SRC_CH>"Source Output<br/>Channel FULL"]
    SRC_CH -->|blocked| MAP["Map Operator<br/>blocked on write"]
    MAP -->|blocked| YAMUX_OUT["Yamux Stream<br/>window closes"]
    YAMUX_OUT -->|blocked| NET["Network send<br/>pauses"]
    NET -->|blocked| YAMUX_IN["Yamux Stream<br/>stops reading"]
    YAMUX_IN -->|blocked| WIN_CH>"Window Input<br/>Channel FULL"]
    WIN_CH -->|blocked| WIN["Window Operator<br/>blocked on write"]
    WIN -->|blocked| SINK_CH>"Sink Input<br/>Channel FULL"]
    SINK_CH -->|blocked| SINK["Slow Sink<br/>ROOT CAUSE"]

    style SINK fill:#f66,stroke:#333,color:#fff
    style KAFKA fill:#ff9,stroke:#333,color:#333
```

---

## 8. Watermark Propagation

How watermarks flow through multi-input operators using the min-watermark rule.

```mermaid
sequenceDiagram
    participant SA as Source A<br/>(Kafka P0)
    participant SB as Source B<br/>(Kafka P1)
    participant J as Join Operator
    participant W as Window Operator

    SA->>J: Events (T=90..100)
    SA->>J: Watermark(T=100)
    SB->>J: Events (T=80..85)
    SB->>J: Watermark(T=85)

    Note over J: Output Watermark =<br/>min(100, 85) = 85

    J->>W: Watermark(T=85)

    Note over W: Trigger all windows<br/>where Window_End < 85<br/>Purge expired state<br/>from Pebble

    W->>W: Emit window results
```

---

## 9. State Backend & Checkpoint Lifecycle

The Pebble checkpoint mechanics and durable storage layout.

```mermaid
flowchart TD
    subgraph Operator Processing
        BAR[Barrier N arrives] --> FLUSH[Flush Pebble MemTable]
        FLUSH --> LINK["Create Checkpoint<br/>Hard-link SSTables<br/>~milliseconds"]
        LINK --> RESUME[Resume Processing<br/>immediately]
    end

    subgraph Async Upload
        LINK --> BG[Background Goroutine]
        BG --> UPLOAD[Upload new/changed<br/>SSTables to S3]
        UPLOAD --> ACK["AcknowledgeCheckpoint N<br/>to Coordinator"]
    end

    subgraph Coordinator
        ACK --> CHECK{All tasks<br/>ACK'd?}
        CHECK -->|No| WAIT[Wait for remaining]
        CHECK -->|Yes| COMPLETE["Global Checkpoint N<br/>Complete"]
    end

    subgraph S3 Layout
        direction TB
        BUCKET["s3://bucket/jobs/&lt;job-id&gt;/checkpoints/"]
        CHK1["chk-1/"]
        CHK2["chk-2/"]
        META["metadata.json<br/>(Graph Topology)"]
        T0["task-0-state/<br/>(SSTables)"]
        T1["task-1-state/<br/>(SSTables)"]
        BUCKET --> CHK1
        BUCKET --> CHK2
        CHK1 --> META
        CHK1 --> T0
        CHK1 --> T1
    end
```

---

## 10. Task Lifecycle State Machine

The state transitions for a Task during its lifetime.

```mermaid
stateDiagram-v2
    [*] --> CREATED
    CREATED --> DEPLOYING : Schedule on Worker

    DEPLOYING --> RUNNING : Binary deployed,<br/>state restored
    DEPLOYING --> FAILED : Deploy error

    RUNNING --> FINISHED : Stream ends
    RUNNING --> FAILED : Exception / OOM
    RUNNING --> CANCELED : User cancel /<br/>Job failure
    RUNNING --> PAUSED : Debug / Rescale

    PAUSED --> RUNNING : Resume

    FAILED --> [*]
    FINISHED --> [*]
    CANCELED --> [*]
```

---

## 11. Rescaling Process

How Wire handles changing parallelism (stop-start, not hot scaling).

```mermaid
flowchart TD
    A[Operator triggers<br/>manual Savepoint] --> B[Global Checkpoint<br/>Savepoint created]
    B --> C[Stop Job:<br/>Cancel all tasks]
    C --> D["Update Config:<br/>parallelism 4 to 8"]
    D --> E[Resubmit Job<br/>pointing to Savepoint]
    E --> F[Coordinator recalculates<br/>Key Group assignments]
    F --> G[New workers download<br/>their Key Group shards]
    G --> H[Sources rewind to<br/>Savepoint offsets]
    H --> I[Processing resumes<br/>with new parallelism]
```

---

## 12. Connector Ecosystem

Sources and Sinks available in Wire.

```mermaid
flowchart LR
    subgraph Sources
        direction TB
        SK[Apache Kafka]
        SS[AWS SQS]
        SR[RabbitMQ]
        S3S[S3 / MinIO]
        SH[HTTP / Webhooks]
    end

    subgraph Wire Engine
        direction TB
        P["Pipeline:<br/>Map - Filter - KeyBy<br/>Window - Aggregate"]
    end

    subgraph Sinks
        direction TB
        OK[Apache Kafka]
        OP[PostgreSQL]
        OM[MongoDB]
        OR[Redis]
        OE[Elasticsearch]
        OS[S3 / MinIO]
    end

    SK --> P
    SS --> P
    SR --> P
    S3S --> P
    SH --> P

    P --> OK
    P --> OP
    P --> OM
    P --> OR
    P --> OE
    P --> OS
```
