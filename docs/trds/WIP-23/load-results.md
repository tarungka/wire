# WIP-23 load verification — 2026-09-12

Implementation revision: `0c4cd98` (Fix 3). Host: Darwin arm64, Go 1.25.0,
k6 1.0.0. A fresh coordinator data directory and one example worker with four
task slots were used. Docker was unavailable, so binaries ran directly on the
host; this is not a measurement of the original Docker deployment.

The unchanged `examples/observability-stack/k6/submit_jobs.js` ran at 100 RPS
for two minutes with `PRE_VUS=500`, `MAX_VUS=2000`, and the real graph from
`examples/print-uppercase-graph`. Coordinator metrics were captured before,
after, and every five seconds during the run. Only test-owned processes were
stopped afterward.

| Measurement | Result | Proposal target |
| --- | --- | --- |
| Submissions | 12,000 HTTP 201; zero failed/interrupted iterations | Sustained 100 RPS |
| HTTP submit p99 | 18.93 ms (k6 threshold output) | <30 ms: met |
| Heartbeat p99 | Estimated 0.40 ms; bucket upper bound 0.50 ms; 25 calls | <10 ms: met in this sample |
| UpdateTaskStatus p99 | Estimated 49.79 ms; bucket spans 10–50 ms; 208 calls | <10 ms: not met |
| Synchronous write_batch p99 | Estimated 8.90 ms; bucket upper bound 10 ms; 12,000 calls | Approximately 6–12 ms: consistent |
| Advisory write_batch_async p99 | Estimated 0.0496 ms; bucket upper bound 0.05 ms; four calls | No separate target |
| Coordinator goroutines | 37 before load; 536–538 in later samples, maximum 538 | <100: not met |

RPC and Pebble quantiles use linear interpolation of cumulative histogram
bucket deltas between the before/after scrapes. They are estimates, not exact
request timings. In particular, 49.79 ms does not mean the measured status
updates individually clustered near 50 ms. The 10–50 ms bucket is too coarse
for that inference, but the counts do establish that the <10 ms target failed.

The 500 preallocated k6 users may contribute idle HTTP connection goroutines;
this run did not profile goroutine stacks, so their cause is not established.
No before/after baseline was measured on this host, and the advisory write
sample is small. These results do not establish a causal speedup from Fix 3.
They also measure submission acknowledgement, not completion of all submitted
jobs. WIP-23 remains Partially Implemented while its outstanding targets are
investigated.

## Reproduction

Build `./cmd`, `./examples/wire-worker-example`, and
`./examples/print-uppercase-graph`. Run the coordinator with a fresh data
directory, noop election, metrics enabled, and separate loopback HTTP/RPC
ports. Run the example worker against that RPC port with four slots. Capture
`/metrics` before and after this command, and every five seconds while it runs:

```sh
WIRE_API=http://127.0.0.1:24001 \
GRAPH_BYTES="$(/path/to/print-uppercase-graph)" \
RPS=100 PRE_VUS=500 MAX_VUS=2000 DURATION=2m \
k6 run --summary-export=summary.json \
  examples/observability-stack/k6/submit_jobs.js
```

Preserve k6 console output as well as summary JSON: k6's default JSON trend
summary in this version omits p99, but the threshold output reports it.
