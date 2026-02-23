# What Wire's Documentation Is Lacking

**Status:** Reference
**Version:** 0.1.0
**Context:** Gap analysis of Wire's current 6 docs — what's covered well, what's missing, what's vague, and what contradicts the codebase.

---

## What The Docs Do Well

The 5 Canon docs (vision, architecture, execution-model, state-backend, operations) are strong internal engineering specs. They cover:
- Core guarantees (EOS, deterministic recovery, strict ordering)
- Fault tolerance model (ABS/Chandy-Lamport) with step-by-step barrier alignment
- State backend design (Pebble, key encoding, async snapshot protocol, S3 layout)
- Event/time/watermark semantics
- Backpressure cascade
- Task lifecycle state machine
- Deployment and monitoring basics

These are well-written, consistent with each other, and implementable.

---

## What's Lacking

### Category 1: No User-Facing Documentation At All

These topics are completely absent — no doc covers them.

1. **User API / SDK** — How does someone write a Wire job? There is no DataStream API, no Pipeline DSL, no YAML schema, no Go SDK, nothing. The docs describe the engine internals but never explain how a user interacts with Wire.

2. **Configuration Reference (`wire.yaml`)** — Referenced in operations.md ("Configuration via `wire.yaml`") but never documented. No schema, no example, no field descriptions.

3. **CLI Reference** — `wire coordinator` and `wire worker` mentioned as commands but no `--help` output, no flag reference, no usage examples. (The code has ~720 lines of flag parsing in `cmd/init.go` that isn't documented.)

4. **Connector Documentation** — Wire's only planned connector is HTTP API (push/pull). The technical doc references this but provides zero detail — no interfaces, no configuration, no usage examples. The Go interfaces for Source/Sink don't exist in code or docs. The Connector SDK (WIP-16) is planned but not yet specified.

5. **Wire Protocol / Serialization** — No specification for the binary format used between nodes. What does a message look like on the wire? What serialization (protobuf? msgpack? custom?)? The code has msgpack utilities but no protocol spec.

6. **Security** — The technical doc has empty headings for "Data Encryption" and "Secret Management" with questions but no answers. mTLS, auth, RBAC — all absent.

7. **Error Handling & Dead Letter Queue** — No documented strategy for handling processing errors, poison messages, or routing failed events. The Gemini research recommended first-class DLQ; nothing was captured.

8. **Job Lifecycle** — No documentation on how jobs are submitted, started, paused, canceled, or upgraded. No REST API spec.

9. **Glossary** — Terms like "Key Group", "Epoch", "Barrier", "Task Slot" are used throughout but never formally defined in one place.

### Category 2: Documented But Vague (Not Implementable)

These topics are mentioned but lack enough detail to code from.

10. **RPC Interface** — architecture.md lists 4 RPC functions (`SubmitJob`, `UpdateTaskStatus`, `TriggerCheckpoint`, `AcknowledgeCheckpoint`) but provides no signatures, request/response types, or error semantics.

11. **Coordinator HA** — The coordinator is "lightweight and generally stateless (relying on an external metadata store or leader election for HA)" — but which metadata store? What leader election? Raft is in go.mod but not in the docs.

12. **Two-Phase Commit for Sinks** — execution-model.md says exactly-once requires "transactional or idempotent" sinks but never defines the 2PC protocol, pre-commit/commit hooks, or how it integrates with checkpointing.

13. **Barrier Alignment Timeout** — What happens if a barrier never arrives on one input? The docs describe the happy path but not the failure case. Does the checkpoint timeout? Is the job killed?

14. **Source Watermark Generation** — "Based on observed data (monotonically increasing)" but what algorithm? Periodic? Per-record? Bounded out-of-orderness? This is critical for correctness.

15. **State Sharding / Key Group Assignment** — Key Groups mentioned as "the atomic unit for redistribution" but the assignment algorithm (how many groups? hash function? rebalancing logic?) is unspecified.

16. **Goroutine Model** — How many goroutines per Task Slot? Bounded pool or unbounded? This affects memory and scheduling characteristics.

17. **Heartbeat Configuration** — "Timeout triggers Job Failure" but what interval? What timeout? Configurable?

18. **Checkpoint Metadata Format** — `metadata.json` referenced in the S3 layout but its schema is never defined.

19. **Broadcast State** — Listed as a state type in state-backend.md ("Configuration data sent to all parallel instances") but no API, no update mechanism, no consistency guarantees.

20. **Late Data / Allowed Lateness** — Mentioned as "configurable grace period" but no configuration syntax, no units, no per-operator scoping, no side-output mechanism.

### Category 3: The Technical Documentation Is Essentially Empty

21. **`techinical-documentation.md` is ~5% complete.** It reads as a table of contents with questions rather than answers. Every section beyond the executive summary is a placeholder:
    - "Where does the Coordinator keep its metadata?" — question, no answer
    - "Map out how many goroutines are spawned" — request, no content
    - "Define the binary format" — request, no spec
    - "Include a YAML schema example" — request, no schema
    - "Detailed Go definitions for Source and Sink interfaces" — request, no code

This file is labeled Draft v0.1.0 but is functionally an outline, not documentation.

### Category 4: Docs vs. Code Contradictions

22. **Pebble vs. actual dependencies** — Docs commit to Pebble as the state backend, but go.mod imports BadgerDB (`dgraph-io/badger/v4`) and BoltDB (`go.etcd.io/bbolt`). The code that used these was deleted in the rewrite commits, but go.mod still lists them. The docs don't acknowledge multiple backend options.

23. **AGENTS.md references non-existent code** — References `internal/service/http/`, `internal/service/cluster/`, and other paths that don't exist. This file is stale.

24. **Connectors not yet implemented** — The only planned connector is HTTP API (push/pull). No connector code exists yet and no interfaces are defined. Additional connectors will be user-built via the Connector SDK (WIP-16).

---

## Priority Ranking

| Priority | Gap | Why It Matters |
|----------|-----|----------------|
| **P0** | User API / SDK | Can't use Wire without knowing how to write jobs |
| **P0** | Connector interfaces (Source/Sink) | Can't process data without connectors |
| **P0** | Configuration reference | Can't run Wire without knowing how to configure it |
| **P0** | Job lifecycle / submission | Can't submit or manage jobs |
| **P1** | RPC interface spec | Can't implement coordinator-worker communication |
| **P1** | Wire protocol / serialization | Can't implement inter-node data transport |
| **P1** | Coordinator HA | Can't run Wire in production |
| **P1** | 2PC for transactional sinks | Can't deliver exactly-once to external systems |
| **P1** | Security (mTLS, auth) | Can't run Wire in production |
| **P2** | Barrier alignment timeout | Edge case but affects correctness |
| **P2** | Watermark generation algorithm | Affects correctness |
| **P2** | Key Group assignment | Affects rescaling correctness |
| **P2** | Error handling / DLQ | Affects operability |
| **P2** | Late data side outputs | Feature completeness |
| **P3** | Glossary | Developer onboarding |
| **P3** | Goroutine model details | Performance tuning |
| **P3** | Heartbeat config | Operational tuning |
| **P3** | Checkpoint metadata schema | Implementation detail |
