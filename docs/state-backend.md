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
3.  **Broadcast State:** A design concept; the SDK does not currently expose a managed broadcast-state API.

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
    NewIterator(prefix []byte) StateIterator

    // Lifecycle
    Checkpoint(checkpointID uint64) (SnapshotHandle, error)
    Restore(handle SnapshotHandle) error
    Close() error
}
```

---

## 3. Pebble Implementation (Default)

Wire uses **Pebble** (by CockroachDB) as the default engine state backend. The factory also supports `hashmap`, an in-memory sorted-slice backend with serialized snapshots and an optional logical payload memory limit. See [WIP-18](trds/WIP-18/README.md) and the [backend factory](../internal/engine/state_backend_factory.go) for configuration and scope.

### 3.1 Why Pebble?
*   **Go Native:** No CGO overhead (unlike RocksDB), simpler cross-compilation.
*   **LSM Tree:** Optimized for high write throughput (streaming workloads).
*   **Deletion:** The backend exposes per-key deletion. Wire does not currently call Pebble range deletes for window cleanup; see [window state scope](execution-model.md#32-state-scope).
*   **Local Snapshots:** Pebble can reuse SSTables through hard links; hashing and portable export still perform file I/O.

### 3.2 Disk Layout
Each backend instance owns a configured directory and lock. Pebble restore uses
generation directories selected by an `ACTIVE` file, allowing verified state to
replace the previous generation. Paths are configured by the embedding runtime;
`/data/wire/worker-1/job-abc/task-3/pebble-db` is an illustrative path, not a
hard-coded default. See the [implementation](../internal/engine/state_backend_pebble.go).

### 3.3 Key Encoding

Encoding depends on the state implementation. SDK managed state uses a state-kind
byte followed by length-prefixed user key and state name; see
[`backend_state.go`](../sdk/backend_state.go). Key-group redistribution requires
state that implements the key-group restore contract. Pebble key-group range
scans expect a two-byte big-endian group prefix. An arbitrary SDK state snapshot
must not be assumed redistributable simply because it uses Pebble. See
[rescale safety](rescale-safety.md).

## 4. Checkpointing Mechanics

The interaction between the Execution Model and Pebble is critical.

### 4.1 The Async Snapshot Protocol
At an aligned barrier, the operator chain captures immutable checkpoint state
through operator snapshot hooks. For Pebble-backed state:

1. `Checkpoint` calls Pebble with `WithFlushedWAL` to create a consistent directory.
2. The backend hashes the snapshot files and returns a manifest handle. Capture
   and hashing are synchronous; duration depends on state size and storage.
3. The runtime's bounded uploader replicates immutable checkpoint artifacts
   asynchronously. Portable Pebble archives include a manifest and state files.
4. Acknowledgement follows successful replication, not merely queue admission.

Hard links can reduce local snapshot copying, but the implementation does not
promise constant-time capture or incremental network uploads. See the
[checkpoint uploader](../internal/engine/checkpoint_upload.go),
[portable export](../internal/engine/state_snapshot_export.go), and
[WIP-02](trds/WIP-02/README.md).

### 4.2 Durable Storage Organization
The following is a conceptual checkpoint layout, not a fixed deployment path.
The [metadata schema](trds/WIP-06/README.md) records task state files, backend
types, source offsets, and transaction handles. Peer replication transfers
checkpoint artifacts; it is not live replication of the coordinator Pebble DB.
Actual artifact paths are carried by checkpoint manifests:


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
