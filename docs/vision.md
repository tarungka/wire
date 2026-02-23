# Vision & System Contract

**Status:** Canon
**Version:** 1.0.0
**Architectural Era:** Chandy-Lamport / Pebble

---

## 1. What Wire Is

Wire is a **cloud-native, distributed, stateful stream processing engine** written in Go.

It is designed to execute dataflow graphs with **strict correctness guarantees**. Unlike message queues (Kafka, RabbitMQ) which move data, or batch processors (Spark) which process bounded data at rest, Wire processes unbounded data streams continuously with **deterministic state management**.

Wire is architected to be:
1.  **Correct:** It prioritizes consistency and data integrity over "best-effort" throughput.
2.  **Embeddable & Standalone:** It compiles to a single binary with zero external dependencies (no ZooKeeper, no Etcd).
3.  **Cloud Native:** Designed for ephemeral infrastructure where worker failures are nominal, not exceptional.

## 2. What Wire Is Not

*   **Not a Message Queue:** Wire does not store data streams indefinitely. It processes them.
*   **Not a Database:** While Wire manages state (via Pebble), it is not a general-purpose queryable database. State is accessed only via the streaming pipeline.
*   **Not a Batch Engine:** While it can process files, its core physics are event-driven and stream-oriented.

## 3. Core Guarantees

Wire provides the following non-negotiable guarantees to the user:

### 3.1 Exactly-Once Semantics (EOS)
In the event of a failure (node crash, network partition), the effect of processing a record on the system state and output will be reflected **exactly once**.
*   *Note:* This requires compliant sources (replayable) and sinks (transactional or idempotent).

### 3.2 Deterministic Recovery
Recovery is **mechanistic**, not probabilistic.
*   Upon failure, the global graph rolls back to the last successfully completed **Global Snapshot**.
*   State is restored to that consistent point in time.
*   Input streams are rewound to the offsets recorded in that snapshot.

### 3.3 Strict Event Ordering
Within a single keyed partition, events are processed in order. Wire strictly respects `(Key, Timestamp)` causality.

## 4. Execution Time Semantics

Wire operates primarily on **Event Time**.

*   **Event Time:** The time the event actually occurred (embedded in the data).
*   **Processing Time:** The wall-clock time of the machine processing the data.

Wire guarantees that **Watermarks** (logical clocks tracking event time progress) flow strictly monotonically through the graph. Window closures and timers are triggered by Watermarks, ensuring results are correct regardless of network lag or out-of-order delivery.

## 5. Fault Tolerance Model

Wire rejects the "Log Replication" (Raft/Paxos) model for data processing in favor of **Asynchronous Barrier Snapshots (ABS)** (Chandy-Lamport variant).

*   **Availability:** During normal operation, workers process at network speed without consensus overhead.
*   **Consistency:** Consistency is achieved via periodic, coordinated checkpoints.
*   **Trade-off:** We accept a rollback (latency penalty) on failure to gain maximum throughput during health.

## 6. State Consistency

State in Wire (e.g., "current sum of clicks for user X") is:
1.  **Local:** Stored on the worker processing Key X (using Pebble).
2.  **Partitioned:** Keys are strictly assigned to specific "Task Slots".
3.  **Versioned:** Every checkpoint creates an immutable version of the state.

There is no "global shared state". State is always co-located with computation.
