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
Draft --> In Review --> Approved --> Partially Implemented --> Implemented --> Superseded
  |
  +--> Rejected
```

| Status | Meaning |
|--------|---------|
| **Draft** | Under discussion, open for feedback |
| **In Review** | Formally submitted for review |
| **Approved** | Approved for implementation |
| **Partially Implemented** | Some scoped work has landed; remaining implementation or specification differences are recorded in the WIP |
| **Implemented** | Scoped work has landed; explicitly deferred follow-ups are not implied complete |
| **Proposed** | Initial proposal without an implementation |
| **Rejected** | Not moving forward |
| **Superseded** | Replaced by a newer WIP |

Implementation labels describe observed code, not evidence of a formal approval decision. A component can be implemented without providing an end-to-end cluster guarantee. Read each WIP's dated implementation section before relying on a feature.

## Creating a New WIP

1. Create a new folder: `mkdir docs/trds/WIP-XX`
2. Copy the structure from an existing WIP's README.md
3. Fill in all header fields and sections
4. Open a PR for review and discussion

---

## WIP Index

All 25 proposals, initially audited against `master` at `0e78195` on 2026-09-12 and updated as scoped work lands. Statuses match the individual WIP headers; original problem statements may describe an earlier codebase.

### Engine Core

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-01](WIP-01/README.md) | Wire Protocol & Serialization Format | Implemented (CRC latency check explicitly waived; see WIP) |
| [WIP-02](WIP-02/README.md) | Goroutine & Concurrency Model | Implemented |
| [WIP-03](WIP-03/README.md) | Key Group Assignment & State Sharding | Partially Implemented |
| [WIP-04](WIP-04/README.md) | Watermark Generation Algorithm | Partially Implemented |
| [WIP-05](WIP-05/README.md) | Barrier Alignment Timeout & Failure Handling | Partially Implemented |
| [WIP-06](WIP-06/README.md) | Checkpoint Metadata Schema | Partially Implemented |
| [WIP-18](WIP-18/README.md) | Multiple State Backends | Partially Implemented |

### Runtime Infrastructure

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-07](WIP-07/README.md) | RPC Interface Specification | Partially Implemented |
| [WIP-08](WIP-08/README.md) | Heartbeat & Health Monitoring | Partially Implemented |
| [WIP-09](WIP-09/README.md) | Coordinator High Availability | Partially Implemented |
| [WIP-10](WIP-10/README.md) | Two-Phase Commit for Transactional Sinks | Partially Implemented |
| [WIP-11](WIP-11/README.md) | Error Handling & Dead Letter Queues | Partially Implemented |
| [WIP-12](WIP-12/README.md) | Late Data & Allowed Lateness | Partially Implemented |
| [WIP-20](WIP-20/README.md) | Task Execution Engine | Implemented |

### User-Facing Layer

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-13](WIP-13/README.md) | Configuration Reference | Partially Implemented |
| [WIP-14](WIP-14/README.md) | User API & Go SDK | Partially Implemented |
| [WIP-15](WIP-15/README.md) | Job Lifecycle & REST API | Partially Implemented |
| [WIP-16](WIP-16/README.md) | Connector SDK & Built-in Connectors | Partially Implemented |
| [WIP-19](WIP-19/README.md) | YAML Pipeline Parser | Partially Implemented |

### Security

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-17](WIP-17/README.md) | Security Model | Partially Implemented |

### Command Dispatch and Correctness/Performance Fixes

| WIP | Title | Status |
|-----|-------|--------|
| [WIP-21](WIP-21/README.md) | Push-Based Command Dispatch | Implemented |
| [WIP-22](WIP-22/README.md) | RPC Duration Histogram for Streaming Calls | Implemented |
| [WIP-23](WIP-23/README.md) | Coordinator Submit Lock/Fsync Contention | Partially Implemented |
| [WIP-24](WIP-24/README.md) | TaskSlot Operator-Error Propagation | Implemented |
| [WIP-25](WIP-25/README.md) | Constant-Time Active Job Name Lookup | Implemented |

## Implementation review — 2026-09-12

The status tables above describe `master`. Open PRs below are proposed increments,
not evidence that the entire WIP is complete. Each PR records its implemented
scope, validation, and remaining work. The initial status audit merged in
[#187](https://github.com/tarungka/wire/pull/187).

WIPs **20, 21, 22, 24, and 25** are implemented on `master`. WIP-20's lifecycle
completion merged in [#188](https://github.com/tarungka/wire/pull/188).

| WIP | Individual PR | Scope of this increment |
| --- | --- | --- |
| [WIP-01](WIP-01/README.md) | [#210](https://github.com/tarungka/wire/pull/210), follows #208 | Full scope; CRC latency check explicitly waived |
| [WIP-02](WIP-02/README.md) | [#211](https://github.com/tarungka/wire/pull/211), follows #207/#149 | Concurrency runtime, checkpoint replication and recovery; validated in PR, review/merge pending |
| [WIP-03](WIP-03/README.md) | [#212](https://github.com/tarungka/wire/pull/212) | Distributed keyed routing and savepoint state redistribution; follows #150 and #206 |
| [WIP-04](WIP-04/README.md) | [#205](https://github.com/tarungka/wire/pull/205) | Startup idle timeout |
| [WIP-05](WIP-05/README.md) | [#204](https://github.com/tarungka/wire/pull/204) | Abort cleanup at failure thresholds |
| [WIP-06](WIP-06/README.md) | [#203](https://github.com/tarungka/wire/pull/203) | Checkpoint manifest validation |
| [WIP-07](WIP-07/README.md) | [#202](https://github.com/tarungka/wire/pull/202) | Concurrent RPC session shutdown |
| [WIP-08](WIP-08/README.md) | [#201](https://github.com/tarungka/wire/pull/201) | Live-worker placement |
| [WIP-09](WIP-09/README.md) | [#200](https://github.com/tarungka/wire/pull/200) | Recovery fencing metadata validation |
| [WIP-10](WIP-10/README.md) | [#199](https://github.com/tarungka/wire/pull/199) | Local transaction boundaries |
| [WIP-11](WIP-11/README.md) | [#194](https://github.com/tarungka/wire/pull/194) | Runtime error policies and DLQ sinks |
| [WIP-12](WIP-12/README.md) | [#193](https://github.com/tarungka/wire/pull/193) | Window lateness and snapshots |
| [WIP-13](WIP-13/README.md) | [#195](https://github.com/tarungka/wire/pull/195) | Configuration reference and substitution |
| [WIP-14](WIP-14/README.md) | [#198](https://github.com/tarungka/wire/pull/198) | Ordered SDK window execution |
| [WIP-15](WIP-15/README.md) | [#196](https://github.com/tarungka/wire/pull/196) | Job and savepoint CLI |
| [WIP-16](WIP-16/README.md) | [#191](https://github.com/tarungka/wire/pull/191) | HTTP ingest and delivery connectors |
| [WIP-17](WIP-17/README.md) | [#197](https://github.com/tarungka/wire/pull/197) | Runtime HTTPS and worker RPC TLS |
| [WIP-18](WIP-18/README.md) | [#190](https://github.com/tarungka/wire/pull/190) | Pebble state backend and SDK state |
| [WIP-19](WIP-19/README.md) | [#192](https://github.com/tarungka/wire/pull/192) | YAML parsing and CEL transforms |
| [WIP-23](WIP-23/README.md) | [#189](https://github.com/tarungka/wire/pull/189) | Metadata durability and heartbeat fsync reduction; merged, targets remain unmet |

WIP-14's PR #198 is stacked on WIP-12's PR #193. Review/merge #193 first, then
retarget #198 to `master`. The repository's CI workflows only run for PRs targeting
`main` or `master`, so #198's local race-enabled integration validation must not be
represented as a successful GitHub CI run. Recheck CI after retargeting.

No other open PR in this table is merged by this audit. The existing WIP-26
optimization proposal is tracked separately in
[#178](https://github.com/tarungka/wire/pull/178); it is not part of the WIP-01–25
catalog on `master`.
