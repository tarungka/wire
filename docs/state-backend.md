# State Backend

**Status:** Canon
**Version:** 1.0.0
**Context:** Persistence & Storage

---

## 1. State Model

State in Wire is not a "sidecar" cache; it is a first-class citizen embedded in the processing pipeline.

### 1.1 Types of State
1.  **Keyed State:** Available only on keyed streams (`KeyBy()`).
    *   Scoped to the current partition key.
    *   Examples: `ValueState`, `ListState`, `MapState`.
2.  **Operator State:** Bound to a parallel task instance.
    *   Examples: Source read positions (e.g., HTTP API sequence numbers).
3.  **Broadcast State:** Configuration data sent to all parallel instances.

---

## 2. State Backend Abstraction

To ensure decoupling, Wire defines a strict interface for storage engines.

```go
type StateBackend interface {
    // Key-Value operations
    Put(key []byte, value []byte) error
    Get(key []byte) ([]byte, error)
    Delete(key []byte) error
    
    // Range Scans (Crucial for Windowing)
    NewIterator(prefix []byte) Iterator

    // Lifecycle
    Checkpoint(checkpointID int64) (SnapshotHandle, error)
    Restore(handle SnapshotHandle) error
    Close() error
}
```

---

## 3. Pebble Implementation (Default)

Wire uses **Pebble** (by CockroachDB) as the default embedded storage engine.

### 3.1 Why Pebble?
*   **Go Native:** No CGO overhead (unlike RocksDB), simpler cross-compilation.
*   **LSM Tree:** Optimized for high write throughput (streaming workloads).
*   **Range Deletes:** Efficient cleanup of expired window state.
*   **Incremental Checkpoints:** Hard-link based snapshots are nearly instantaneous.

### 3.2 Disk Layout
*   Each Task Slot has its own dedicated Pebble directory:
    `/data/wire/worker-1/job-abc/task-3/pebble-db`
*   This isolation prevents "noisy neighbor" contention at the DB lock level.

### 3.3 Key Encoding
To map logical stream state to Pebble's KV store, we use a composite key:

`[KeyGroupPrefix][OperatorID][UserKey][Namespace/Window]`

*   **KeyGroupPrefix:** Allows fast rescaling (moving ranges of keys between workers).
*   **Window:** Allows efficient range scans to find expired windows.

---

## 4. Checkpointing Mechanics

The interaction between the Execution Model and Pebble is critical.

### 4.1 The Async Snapshot Protocol
When a Task receives a **Barrier**, it triggers `backend.Checkpoint()`:

1.  **Flush:** Pebble MemTables are flushed to disk (optional, or just included in log).
2.  **Link:** Pebble creates a "Checkpoint" (directory of hard links to SSTables). This takes milliseconds.
3.  **Resume:** The Task resumes processing immediately.
4.  **Replicate (Async):** A background Goroutine walks the Checkpoint directory and replicates new/changed SSTables to the durable store (replicated PebbleDB on peer nodes via the wire protocol; see WIP-09).

### 4.2 Durable Storage Organization
Snapshots are stored in a durable storage layout:

```
<data-dir>/jobs/<job-id>/checkpoints/
    chk-1/
        metadata.json  (Global Graph Topology)
        task-0-state/  (SSTables)
        task-1-state/  (SSTables)
    chk-2/
        ...
```

---

## 5. Consistency Guarantees

*   **Snapshot Isolation:** Pebble provides a consistent view of the database at the moment `Checkpoint()` was called.
*   **Atomic Restore:** On recovery, the database is completely replaced by the restored snapshot before processing resumes. Wire does not support "partial" restores that mix old and new state.
