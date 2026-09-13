# WIP-02 completion audit

Baseline: master `5d7ac4b` (includes merged WIP-01 PR #210). This follow-up
supersedes the limited barrier-identity change in #207 and initial #149.
Status remains Partially Implemented until all applicable items below have
implementation and executable evidence. The WIP-01 CRC latency waiver does not
waive any WIP-02 requirement.

## Acceptance scenarios (§8.1)

| # | Requirement | Current evidence / remaining work |
|---|---|---|
| 1 | Slow sink bounds source reading | Existing TaskSlot backpressure tests; audit network and output fan-out bounds. |
| 2 | Cancellation drains and joins within five seconds | Produced-output drain now uses DrainTimeout; input-close and output-close helpers are joined. Network regressions cover a resumed receiver and a permanently blocked receiver. Input-side drain semantics and full lifecycle/resource audit remain open. |
| 3 | Async checkpoint replication permits continued processing | Operator.Checkpoint results are discarded. No TaskSlot upload worker or worker integration exists. Implement bounded, owned replication and failure handling. |
| 4 | Concurrent source read/watermark safety | Separate source reader and legacy watermark strategy exist; audit source implementations and add full-lifecycle race evidence. |
| 5 | Two-input alignment / snapshot / release | WIP-01 ordering and atomic buffer transfer regressions exist. Retain pre-barrier snapshot and barrier-before-post-data ordering. |
| 6 | Abort drains without snapshot | Existing abort tests; audit races with upload completion and shutdown. |
| 7 | Operator panic fails task and joins siblings | Panic recovery and authoritative chain-error handling exist; validate worker FAILED reporting and lifecycle cleanup. |
| 8 | Atomic watermarks | Tracker CAS and emitter concurrency tests exist; audit end-to-end publication semantics. |
| 9 | Prompt control mailbox handling under full data channels | Control-priority test exists; audit blocking output and checkpoint-work interactions. |

## Other explicit requirements

| Requirement | Current evidence / remaining work |
|---|---|
| Per-task topology and bounded channels (§2.1–2.4) | Input reader includes one bounded read-ahead helper. Output uses one ordered dispatcher, not the stated per-output workers. Reconcile topology with ordering and test bounds. |
| Replication failure affects checkpoint, threshold affects task (§2.7) | No replication lifecycle is connected. Integrate with checkpoint failure policy; do not silently convert upload failure to success or unconditional task failure. |
| Configuration (§3.1) | Input/output/alignment sizes and DrainTimeout exist. Upload concurrency and configurable task Pebble compaction bounds need implementation/integration. |
| Container CPU limits (§3.2) | No automaxprocs import or dependency found. Verify Go runtime baseline and implement the documented behavior with explicit evidence. |
| Six observability metrics (§3.3) | No wire_task_* metric instrumentation found. Add task channel usage, output blocking time, owned goroutines, upload duration and alignment bytes with lifecycle cleanup. |
| Benchmarks (§8) | Engine benchmarks exist. Establish and record concurrency/channel/deserialization baselines after implementation. |
| Documentation and PR | Update actual topology, configuration and status only after validation; linked follow-up PR to #207/#149, using personal GitHub account. |

## Specification correction carried forward from WIP-01

The §2.5 diagram still puts side-buffer draining before barrier forwarding.
Those records arrived after the barrier and must remain after it. The WIP-01
implementation and regression preserve the correct ordering; WIP-02 must not
reintroduce the old behavior while implementing asynchronous snapshot work.
