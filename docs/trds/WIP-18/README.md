# Technical Requirements Document (TRD)

> **Feature/Project:** `Checkpoint Metadata Schema`
>
> **WIP ID:** `WIP-18`
>
> **Author:** `Tarun Ashok`
>
> **Status:** `Draft`
>
> **Created:** `2026-02-22`
>
> **Last Updated:** `2026-02-22`

### Revision History

| Version | Date | Author | Changes |
| -- | -- | -- | -- |
| 0.1 | 2026-02-22 | Tarun Ashok | Initial draft |

---

## 1. Overview

### 1.1 Problem Statement

Wire's state-backend.md references a `metadata.json` file in the S3 checkpoint layout but **its schema is never defined**. Without a specified format, checkpoint restoration, savepoint compatibility checks, and tooling (inspection, migration) cannot be implemented. The metadata file is the "table of contents" for a checkpoint — everything depends on its correctness.

### 1.2 Proposed Solution (Technical Summary)

Define the `metadata.json` schema for checkpoints and savepoints. The file contains: checkpoint/savepoint ID, job graph topology, operator-to-task mapping, Key Group assignments, source offsets, and per-task state file manifests. The schema is versioned to support future evolution.

---

## 2. Architecture & System Design

### 2.1 Checkpoint Storage Layout

```
s3://bucket/wire/jobs/<job-id>/
  checkpoints/
    chk-42/
      metadata.json               ← THIS TRD
      task-0-state/
        MANIFEST-000001
        000042.sst
        000043.sst
      task-1-state/
        MANIFEST-000001
        000055.sst
      task-2-state/
        ...
  savepoints/
    sp-1/
      metadata.json               ← Same schema as checkpoints
      task-0-state/
        ...
```

### 2.2 Metadata Role in Recovery

1. Coordinator selects latest completed checkpoint (e.g., `chk-42`).
2. Coordinator reads `chk-42/metadata.json`.
3. From metadata, Coordinator knows:
   - Which operators and tasks exist in the job graph.
   - Which Key Group ranges each task owned.
   - What source offsets to restore.
   - Where each task's state files are located.
4. Coordinator schedules new tasks (potentially with different parallelism).
5. Each new task downloads its assigned Key Group ranges from the checkpoint.

---

## 3. API Design

### 3.1 metadata.json Schema

```json
{
  "schema_version": 1,
  "type": "checkpoint",
  "checkpoint_id": 42,
  "job_id": "job-a1b2c3d4",
  "job_name": "user-event-counter",
  "trigger_time": "2024-01-15T12:00:00Z",
  "completion_time": "2024-01-15T12:00:01.250Z",
  "duration_ms": 1250,

  "job_graph": {
    "num_key_groups": 128,
    "operators": [
      {
        "operator_id": "kafka-source",
        "type": "source",
        "parallelism": 4,
        "chained_to": null
      },
      {
        "operator_id": "parse-map",
        "type": "map",
        "parallelism": 4,
        "chained_to": "kafka-source"
      },
      {
        "operator_id": "user-keyby",
        "type": "keyby",
        "parallelism": 4,
        "chained_to": null
      },
      {
        "operator_id": "count-window",
        "type": "window",
        "parallelism": 4,
        "chained_to": null
      },
      {
        "operator_id": "es-sink",
        "type": "sink",
        "parallelism": 2,
        "chained_to": null
      }
    ]
  },

  "tasks": [
    {
      "task_id": "kafka-source-0",
      "operator_id": "kafka-source",
      "subtask_index": 0,
      "key_group_range": {
        "start": 0,
        "end": 32
      },
      "state_path": "task-0-state/",
      "state_size_bytes": 1048576,
      "state_files": [
        "MANIFEST-000001",
        "000042.sst",
        "000043.sst"
      ],
      "source_offsets": {
        "type": "kafka",
        "partitions": {
          "0": 152340,
          "1": 148921
        }
      }
    },
    {
      "task_id": "kafka-source-1",
      "operator_id": "kafka-source",
      "subtask_index": 1,
      "key_group_range": {
        "start": 32,
        "end": 64
      },
      "state_path": "task-1-state/",
      "state_size_bytes": 983040,
      "state_files": [
        "MANIFEST-000001",
        "000055.sst"
      ],
      "source_offsets": {
        "type": "kafka",
        "partitions": {
          "2": 161502,
          "3": 159877
        }
      }
    }
  ],

  "sink_transactions": [
    {
      "task_id": "es-sink-0",
      "operator_id": "es-sink",
      "committed_checkpoint": 42,
      "transaction_id": "wire-tx-0"
    }
  ]
}
```

### 3.2 Field Definitions

#### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `schema_version` | int | Yes | Metadata schema version (currently `1`) |
| `type` | string | Yes | `"checkpoint"` or `"savepoint"` |
| `checkpoint_id` | int64 | Yes | Unique checkpoint/savepoint identifier |
| `job_id` | string | Yes | Parent job identifier |
| `job_name` | string | Yes | Human-readable job name |
| `trigger_time` | ISO 8601 | Yes | When the checkpoint was triggered |
| `completion_time` | ISO 8601 | Yes | When the last task ACK'd |
| `duration_ms` | int64 | Yes | Total checkpoint duration |

#### Job Graph Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `num_key_groups` | int | Yes | Total Key Groups for this job (immutable) |
| `operators` | []Operator | Yes | All operators in the job graph |
| `operators[].operator_id` | string | Yes | Unique operator identifier (used for savepoint compatibility) |
| `operators[].type` | string | Yes | Operator type (source/map/filter/keyby/window/sink) |
| `operators[].parallelism` | int | Yes | Parallelism at checkpoint time |
| `operators[].chained_to` | string | No | If chained, the parent operator ID |

#### Task Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `task_id` | string | Yes | Unique task identifier (`{operator_id}-{subtask_index}`) |
| `operator_id` | string | Yes | Which operator this task belongs to |
| `subtask_index` | int | Yes | Parallel instance index (0-based) |
| `key_group_range.start` | int | Yes | First Key Group owned (inclusive) |
| `key_group_range.end` | int | Yes | Last Key Group owned (exclusive) |
| `state_path` | string | Yes | Relative path to state files directory |
| `state_size_bytes` | int64 | Yes | Total size of state files |
| `state_files` | []string | Yes | List of Pebble SSTable and manifest files |
| `source_offsets` | object | No | Only for source operators. Connector-specific offset data. |

#### Sink Transaction Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `task_id` | string | Yes | Sink task identifier |
| `operator_id` | string | Yes | Sink operator identifier |
| `committed_checkpoint` | int64 | Yes | Last checkpoint committed to external system |
| `transaction_id` | string | No | External transaction ID (for 2PC recovery) |

### 3.3 Savepoint Compatibility Validation

When restoring from a savepoint with different parallelism:

1. **Must match:** `num_key_groups`, all `operator_id` values for stateful operators.
2. **May differ:** `parallelism` per operator (Key Groups are redistributed per WIP-12).
3. **May be added:** New stateless operators.
4. **May be removed:** Stateless operators.
5. **Must not change:** Operator type for existing operator IDs.

Validation errors:
- `"Operator 'count-window' exists in savepoint but not in new job graph"`
- `"Key Group count mismatch: savepoint=128, job=256"`
- `"Operator type changed: 'my-op' was 'map', now 'filter'"`

---

## 4. Data Model & Storage

### 4.1 Storage Location

| Type | Path Pattern |
|------|-------------|
| Automatic checkpoint | `s3://{bucket}/wire/jobs/{job_id}/checkpoints/chk-{id}/metadata.json` |
| Savepoint | `s3://{bucket}/wire/jobs/{job_id}/savepoints/sp-{id}/metadata.json` |
| Local filesystem | `/var/lib/wire/checkpoints/{job_id}/chk-{id}/metadata.json` |

### 4.2 Size Estimate

For a job with 10 operators, parallelism 8, 80 tasks:
- metadata.json ≈ 10-20 KB (JSON, human-readable)
- Negligible compared to state data (typically MBs to GBs)

---

## 5. Design Decisions & Trade-offs

### Decision 1: JSON format (not binary)

|  |  |
| -- | -- |
| **Context** | Metadata needs to be read by the Coordinator and potentially by humans/tools. |
| **Options Considered** | (A) JSON, (B) Protobuf, (C) msgpack |
| **Decision** | Option A: JSON |
| **Rationale** | Human-readable. Debuggable with `cat` and `jq`. Metadata is small (< 100KB). Parse performance is irrelevant compared to state download time. |
| **Trade-offs Accepted** | Slightly larger than binary. Slower to parse (irrelevant at this size). |
| **Revisit Trigger** | If metadata grows to MBs (unlikely). |

### Decision 2: Schema versioning from day one

|  |  |
| -- | -- |
| **Context** | Metadata schema will evolve. Old checkpoints must remain readable. |
| **Options Considered** | (A) `schema_version` field with forward compatibility, (B) No versioning (break on change), (C) Content-addressable with migration scripts |
| **Decision** | Option A |
| **Rationale** | Simple. Reader checks version and applies appropriate parser. Backwards-compatible changes (new optional fields) don't bump version. Breaking changes bump version with migration logic. |
| **Trade-offs Accepted** | Must maintain parsers for all schema versions indefinitely. |
| **Revisit Trigger** | If schema changes become frequent. |

---

## 6. Edge Cases & Failure Modes

| # | Scenario | Handling | Impact | Severity |
| -- | -- | -- | -- | -- |
| 1 | metadata.json corrupted in S3 | Checkpoint marked invalid. Coordinator falls back to previous checkpoint. | One checkpoint lost | Medium |
| 2 | State files listed in metadata don't exist | Restore fails. Coordinator falls back to previous checkpoint. | One checkpoint lost | Medium |
| 3 | metadata.json written but state upload incomplete (crash mid-checkpoint) | Coordinator detects incomplete checkpoint (missing ACK). Never marked complete. Not used for recovery. | No impact | Low |
| 4 | Unknown `schema_version` in metadata | Error: "Unsupported checkpoint schema version 2. Upgrade Wire." | Cannot restore | High |
| 5 | Savepoint from a different Wire version | Compatible if `schema_version` is supported and operator IDs match. | May work | Medium |

---

## 7. Security & Compliance

### 7.1 Data Protection

* metadata.json contains job configuration references and source offsets but **no event data or state data**.
* Sensitive fields (connection strings, credentials) are **never** stored in metadata. Only operator IDs and structural information.
* Inherits S3 server-side encryption (SSE-S3 or SSE-KMS).

---

## 8. Testing Strategy

| Test Type | Scope | Tools | Coverage Target |
| -- | -- | -- | -- |
| Unit Tests | Schema serialization, deserialization, validation | Go `testing` | 100% |
| Integration Tests | Write checkpoint → read metadata → restore | MiniCluster + S3 mock | Happy path + corruption |
| Compatibility Tests | Metadata from schema_version=1 readable by future versions | Version matrix | All supported versions |

### 8.1 Key Test Scenarios

1. Write metadata → read back → verify all fields match
2. Restore from checkpoint → verify state matches pre-checkpoint state
3. Restore savepoint with different parallelism → verify Key Group redistribution correct
4. Savepoint compatibility check → reject incompatible graph, accept compatible graph
5. Corrupted metadata → fallback to previous checkpoint

---

## 9. Open Questions & Risks

| # | Question / Risk | Owner | Status |
| -- | -- | -- | -- |
| 1 | Should metadata include a checksum of all state files for integrity verification? | Tarun | Open |
| 2 | Should metadata include the full pipeline YAML for reproducibility? | Tarun | Open |
| 3 | Risk: Source offsets are connector-specific (Kafka partitions vs SQS receipts). Need a polymorphic schema. | — | Acknowledged |
