# WIRE Improvement Program (WIPs)

A **WIP** (Wire Improvement Program) is a design document proposing a significant change or addition to Wire. WIPs provide a structured way to discuss, review, and record architectural decisions.

---

## When to Write a WIP

Write a WIP when proposing:
- A new subsystem or major component
- A change to core guarantees or the execution model
- A new connector, API surface, or public-facing interface
- Any change that affects multiple modules or requires cross-cutting coordination

Bug fixes, small refactors, and incremental improvements do not need a WIP.

## Folder Structure

Each WIP lives in its own numbered folder with a `README.md` inside:

```
docs/trds/
  WIP-01/README.md
  WIP-02/README.md
  ...
```

Supporting material (diagrams, benchmarks, prototypes) can be placed alongside the `README.md` in the same folder.

## Status Lifecycle

```
Draft --> In Review --> Approved --> Implemented --> Superseded
  |                                                     ^
  +--> Rejected                                         |
                                            (by a newer WIP)
```

| Status | Meaning |
|--------|---------|
| **Draft** | Under discussion, open for feedback |
| **In Review** | Formally submitted for review |
| **Approved** | Approved for implementation |
| **Implemented** | Fully landed in the codebase |
| **Rejected** | Not moving forward |
| **Superseded** | Replaced by a newer WIP |

## Creating a New WIP

1. Create a new folder: `mkdir docs/trds/WIP-XX`
2. Copy the structure from an existing WIP's README.md
3. Fill in all header fields and sections
4. Open a PR for review and discussion

---

## WIP Index

Ordered by build dependency: engine core first, then runtime infrastructure, then user-facing layer.

### Engine Core

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-01](WIP-01/README.md) | Wire Protocol & Serialization Format | Draft |
| [WIP-02](WIP-02/README.md) | Goroutine & Concurrency Model | Draft |
| [WIP-03](WIP-03/README.md) | Key Group Assignment & State Sharding | Draft |
| [WIP-04](WIP-04/README.md) | Watermark Generation Algorithm | Draft |
| [WIP-05](WIP-05/README.md) | Barrier Alignment Timeout & Failure Handling | Draft |
| [WIP-06](WIP-06/README.md) | Checkpoint Metadata Schema | Draft |

### Runtime Infrastructure

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-07](WIP-07/README.md) | RPC Interface Specification | Draft |
| [WIP-08](WIP-08/README.md) | Heartbeat & Health Monitoring | Draft |
| [WIP-09](WIP-09/README.md) | Coordinator High Availability | Draft |
| [WIP-10](WIP-10/README.md) | Two-Phase Commit for Transactional Sinks | Draft |
| [WIP-11](WIP-11/README.md) | Error Handling & Dead Letter Queues | Draft |
| [WIP-12](WIP-12/README.md) | Late Data & Allowed Lateness | Draft |

### User-Facing Layer

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-13](WIP-13/README.md) | Configuration Reference | Draft |
| [WIP-14](WIP-14/README.md) | User API & Go SDK | Draft |
| [WIP-15](WIP-15/README.md) | Job Lifecycle & REST API | Draft |
| [WIP-16](WIP-16/README.md) | Connector SDK & Built-in Connectors | Draft |

### Cross-Cutting & Reference

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-17](WIP-17/README.md) | Security Model | Draft |
