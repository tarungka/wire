# Architecture

**Status:** Canon
**Version:** 1.0.0
**Context:** Runtime Structure & Components

---

## 1. High-Level Topology

Wire implements a classic **Master-Worker** distributed architecture.

### 1.1 The Coordinator (Control Plane)
The central brain of the cluster. It is lightweight and generally stateless (relying on an external metadata store or leader election for HA).

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
*   A **Task Slot** is a fixed slice of resources (CPU/RAM).
*   Each **Operator** (e.g., `Map`, `Filter`, `Window`) runs as a lightweight Goroutine chain within a slot.
*   **Operator Chaining:** Sequential operators (e.g., `Source -> Map -> Filter`) are fused into a single Goroutine to avoid serialization overhead.

### 2.2 The TCP Mux
Wire uses **HashiCorp Yamux** on Port 4001 for efficient connection multiplexing.

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
The Coordinator optimizes the Logical Graph.
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

### 4.2 Heartbeating
*   Workers send periodic heartbeats to the Coordinator.
*   Timeout triggers a **Job Failure** event -> Recovery Workflow.

### 4.3 Task Lifecycle
State machine for a Task:
`CREATED -> DEPLOYING -> RUNNING -> (PAUSED) -> FINISHED`
                    `\-> FAILED`
                    `\-> CANCELED`

*   **Deploying:** Downloading binary/config and restoring state from Pebble.
*   **Running:** Processing stream.
*   **Paused:** (Optional) During complex rescaling or debugging.
