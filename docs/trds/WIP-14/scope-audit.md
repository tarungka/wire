# WIP-14 completion audit

This audit preserves the original proposal's scope. An entry here is not a
completion claim for the whole WIP. Evidence is from this branch; related open
PRs are dependencies, not merged functionality.

| Original requirement | Current evidence | Remaining work |
| --- | --- | --- |
| DataStream Map, FlatMap, Filter, KeyBy | `sdk/data_stream.go`, graph and MiniCluster error-policy tests | Final API/reference review |
| Union, Connect, branches and per-operator parallelism | `sdk/dag_execution_test.go`, `connected_stream.go`, local registry/driver | Audit named cluster combinations |
| Window Aggregate, Reduce, Apply; late routing | WIP-12 dependency #227; MiniCluster window tests execute all three operations and window kinds | Record dependency in PR |
| Timestamp extraction and watermark strategy | `sdk/dag_execution_test.go`, watermark configuration tests, engine ordered terminal-watermark tests | Public API/reference review |
| Keyed Value/List/Map state and TTL | `sdk/state_ttl_test.go`, `process_runtime_test.go`, hashmap/Pebble tests | None identified in these APIs |
| Process context, timers and side outputs | Process harness and `TestMiniClusterRestoresOffsetsAndManagedState` | None identified in tested behavior |
| Distributed keyed Process | `internal/worker/process_recovery_test.go`, named Process factory adapter | Review public registration instructions |
| Checkpoint interval, timeout, minimum pause | SDK submission-envelope tests; coordinator persistence, trigger and timeout tests | None identified for these settings |
| Maximum concurrent checkpoints API | Original proposal declares `SetMaxConcurrentCheckpoints`; runtime currently enforces one in-flight checkpoint, also required by WIP-05 acceptance | API/contract discrepancy remains open; do not silently ignore a setting |
| Fixed/exponential/no-restart policies | RPC validation/delay tests, coordinator budget and persistence tests, real MiniCluster recovery | None identified for implemented policies |
| State backend selection | SDK remote submission test, deployed memory-limit enforcement, worker pre-factory validation, scoped persistent state tests | WIP-18 broader storage contracts remain to audit |
| TestHarness and MiniCluster | Real workers, replicas and recovery; shutdown joins executions; existing acceptance tests run on new driver | Final documented-API review |
| Runnable examples and walkthrough | `docs/sdk-walkthrough.md`, `sdk/examples/stateful`, normal/recovery mode tests | Add further examples if reference review finds missing workflows |
| YAML schema and identical SDK graph | Existing parser/validation tests; WIP-13 schema work in #228; remaining runtime/reload work assigned WIP-19 | Must verify across the final WIP-13–19 deliverables; do not claim all YAML execution here |
| SDK test coverage ≥80% | `go test ./sdk -coverprofile=...`: 82.8% before walkthrough aliases | Re-measure final branch |
| Authenticated/encrypted submission and secret handling | Depends on WIP-17/WIP-19; current HTTP URL is unencrypted | Must verify during those WIPs; current walkthrough states limitations |

Compatibility: existing fluent methods and context-aware Execute remain intact.
`NewStreamExecutionEnvironment` and `SetMinPauseBetweenCheckpoints` are aliases
for existing behavior. Proposal signatures that differ from the established SDK
are documented using their actual callable forms rather than breaking users to
match pseudocode.

Do not change WIP-14 to Implemented or publish a completion claim until the open
API/contract items above have been resolved and final acceptance evidence recorded.
