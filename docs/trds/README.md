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

| WIP | Priority | Title | Status |
|-----|----------|-------|--------|
| [WIP-01](WIP-01/README.md) | P0 | User API & Go SDK | Draft |
| [WIP-02](WIP-02/README.md) | P0 | Connector SDK & Built-in Connectors | Draft |
| [WIP-03](WIP-03/README.md) | P0 | Configuration Reference | Draft |
| [WIP-04](WIP-04/README.md) | P0 | Job Lifecycle & REST API | Draft |
| [WIP-05](WIP-05/README.md) | P1 | RPC Interface Specification | Draft |
| [WIP-06](WIP-06/README.md) | P1 | Wire Protocol & Serialization Format | Draft |
| [WIP-07](WIP-07/README.md) | P1 | Coordinator High Availability | Draft |
| [WIP-08](WIP-08/README.md) | P1 | Two-Phase Commit for Transactional Sinks | Draft |
| [WIP-09](WIP-09/README.md) | P1 | Security Model | Draft |
| [WIP-10](WIP-10/README.md) | P2 | Barrier Alignment Timeout & Failure Handling | Draft |
| [WIP-11](WIP-11/README.md) | P2 | Watermark Generation Algorithm | Draft |
| [WIP-12](WIP-12/README.md) | P2 | Key Group Assignment & State Sharding | Draft |
| [WIP-13](WIP-13/README.md) | P2 | Error Handling & Dead Letter Queues | Draft |
| [WIP-14](WIP-14/README.md) | P2 | Late Data & Allowed Lateness | Draft |
| [WIP-15](WIP-15/README.md) | P3 | Glossary of Terms | Draft |
| [WIP-16](WIP-16/README.md) | P3 | Goroutine & Concurrency Model | Draft |
| [WIP-17](WIP-17/README.md) | P3 | Heartbeat & Health Monitoring | Draft |
| [WIP-18](WIP-18/README.md) | P3 | Checkpoint Metadata Schema | Draft |
