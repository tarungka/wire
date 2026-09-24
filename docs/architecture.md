# Architecture

**Status:** Canon
**Version:** 1.0.0
**Context:** Runtime Structure & Components

---

## 1. High-Level Topology

Wire implements a classic **Master-Worker** distributed architecture.

### 1.1 The Coordinator (Control Plane)
The central brain of the cluster. It persists metadata locally via PebbleDB and supports a phased HA strategy: pluggable leader election, fencing tokens for split-brain prevention, and recovery from durable storage (see WIP-09).

**Responsibilities:**
*   **Job Management:** Accepts JobGraphs, optimizes them, and schedules execution.
*   **Checkpoint Coordination:** Triggers Barrier injection and tracks snapshot completion.
*   **Resource Management:** Tracks available Task Slots across the cluster.
*   **Failure Recovery:** Detects worker loss and orchestrates job restarts.

### 1.2 The Worker (Data Plane)
The muscle of the system. Workers execute the actual stream processing logic.

**Responsibilities:**
*   **Task Execution:** Runs one or more "Task Slots".
*   **State Management:** Hosts the embedded **Pebble** instances for local state.
*   **Data Transport:** Manages TCP connections to other workers for shuffling data.

---

## 2. Data Plane Design

The Data Plane is designed for maximum throughput and low latency.

### 2.1 Task Slots & Goroutines
*   A **Task Slot** is a unit of task admission and execution. It does not enforce a fixed CPU/RAM reservation.
*   Each **Operator** (e.g., `Map`, `Filter`, `Window`) runs as a lightweight Goroutine chain within a slot.
*   **Operator Chaining:** Sequential operators (e.g., `Source -> Map -> Filter`) are fused into a single Goroutine to avoid serialization overhead.

### 2.2 The TCP Mux
Wire uses **HashiCorp Yamux** for efficient connection multiplexing. The coordinator wire listener defaults to `:4002`; `:4001` is the HTTP API. Worker data listeners use their registered addresses, not a fixed cluster-wide port.

*   **Connection Sharing:** Instead of opening thousands of TCP connections for task-to-task communication, a single persistent TCP connection is maintained between any two Workers.
*   **Logical Streams:** Each data channel (e.g., `Task A -> Task B`) is a lightweight **Yamux Stream** tunneled over the shared connection.
*   **Flow Control:** Yamux provides built-in window-based flow control per stream, which is critical for backpressure propagation.
*   **Keep-Alives:** The protocol handles heartbeating to detect dead peers rapidly.

---

## 3. Execution Graph

Wire transforms user logic into executable physics in three stages:

### 3.1 Logical Graph (StreamGraph)
The high-level DAG defined by the user code.
*   Nodes: Logical Operations (`Map`, `KeyBy`, `Sink`).
*   Edges: Logical Data Streams.

### 3.2 Optimized Graph (JobGraph)
The SDK converts its StreamGraph to a serialized JobGraph. The Coordinator scheduler groups compatible operators into chains and creates physical task descriptors.
*   **Chaining:** Fuses adjacent compatible operators.
*   **Partitioning:** Injects "Shuffle" or "Forward" edges based on key requirements.

### 3.3 Physical Graph (ExecutionGraph)
The actual parallel instances running on workers.
*   If `Parallelism=4`, a single Logical `Map` node becomes 4 Physical `Map` tasks distributed across the cluster.

---

## 4. Control Plane Mechanisms

### 4.1 RPC
Communication between Coordinator and Workers happens via internal RPC (over the TCP Mux or separate control port).
*   `SubmitJob`
*   `UpdateTaskStatus`
*   `TriggerCheckpoint`
*   `AcknowledgeCheckpoint`
*   `WatchCommands` (server-to-worker push stream)

See the [RPC runtime contract](trds/WIP-07/runtime-contract.md) for message types, errors, TLS, and fencing.

### 4.2 Heartbeating
*   Workers send heartbeats every `heartbeat.interval` (default 5s). A local receipt-time deadline of `heartbeat.timeout` (default 30s) marks workers LOST and triggers task/job recovery. Worker contact expiry stops admission and processing, closes transports and exits nonzero. See [WIP-08 runtime contract](trds/WIP-08/runtime-contract.md).
*   Timeout triggers a **Job Failure** event -> Recovery Workflow.

### 4.3 Task Lifecycle
State machine for a Task:
`CREATED -> DEPLOYING -> RUNNING -> FINISHING -> FINISHED`
                    `\-> FAILED`
                    `\-> CANCELED`

*   **Deploying:** Instantiating registered operator factories from configuration and restoring checkpoint state. Automatic binary distribution is not implemented.
*   **Running:** Processing stream.
*   **Finishing:** Completing bounded-source checkpoint/transaction work before reporting successful completion. Task suspension is not implemented by the job pause endpoint; see [usage limits](usage.md#pause-and-resume-limitations).
