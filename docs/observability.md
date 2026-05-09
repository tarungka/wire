# Observability

Wire ships an OpenTelemetry meter provider with a Prometheus scrape
exporter. Metrics are recorded at four subsystem boundaries — HTTP API,
PebbleDB metadata store, RPC server dispatch, and process runtime — with
strictly-bounded label cardinality so the series count stays flat under
load.

Traces are wired through the same OTel SDK but no exporter is attached
yet; that's the next phase.

## Quick start

```sh
# Start the coordinator. Metrics endpoint is on by default at :9090.
./wire --mode=coordinator

# In another terminal:
curl http://localhost:9090/metrics | grep '^wire_'

# Custom port / disable
./wire --mode=coordinator --metrics-addr=:8080
./wire --mode=coordinator --metrics-enabled=false
```

The Prometheus endpoint is on a **separate HTTP server** from the
coordinator's API (`:4001`), so scraping it never competes with API
traffic and can be locked down independently.

## Metrics inventory

| Metric | Type | Labels | Description |
|---|---|---|---|
| `wire_http_requests_total` | Counter | `method`, `route`, `status_class` | HTTP API request count. `route` is the registered pattern (`GET /api/v1/jobs/{job_id}`), never the concrete path. |
| `wire_http_request_duration_seconds` | Histogram | same | HTTP API request duration, exponential buckets from 100 ns to 10 s. |
| `wire_pebble_ops_total` | Counter | `op` | PebbleDB metadata-store operation count. `op` ∈ `{get, set, delete, write_batch, prefix_scan}`. |
| `wire_pebble_op_duration_seconds` | Histogram | `op` | PebbleDB op duration. |
| `wire_pebble_errors_total` | Counter | `op` | PebbleDB ops that returned an error (excludes `ErrNotFound` on Get). |
| `wire_rpc_server_requests_total` | Counter | `method` | RPC requests served. `method` is the symbolic name from `MethodName()` (`Heartbeat`, `SubmitJob`, ...). |
| `wire_rpc_server_duration_seconds` | Histogram | `method` | RPC handler dispatch duration (read frame → handler → write response). |
| `wire_rpc_server_errors_total` | Counter | `method` | RPC requests that returned an `*RPCError`. |

Plus the standard process metrics from the Go runtime collector:
`process_cpu_seconds_total`, `process_resident_memory_bytes`,
`go_goroutines`, `go_memstats_*`, etc.

## Cardinality discipline

Every label value comes from a fixed enum:
- `method` — the eight `MethodID` names from `internal/rpc/codec.go`
- `route` — patterns registered on the HTTP mux (15 today)
- `op` — five PebbleStore op names
- `status_class` — `1xx` / `2xx` / `3xx` / `4xx` / `5xx`
- `method` (HTTP) — the seven HTTP verbs

Worst case: 15 × 7 × 5 = 525 HTTP series, 5 PebbleStore series, 8 RPC
series. Total stays well under 1 K series. **No raw user input ever
becomes a label.** Adding new instruments should follow the same rule.

## Histogram bucket choice

The default OTel SDK boundaries cap at 10 000 of-unit, which assumes
units are in milliseconds. We declare durations in seconds, so
`internal/observability/otel.go` installs an explicit-bucket view that
spans **100 ns → 10 s** in 17 boundaries. That covers PebbleDB ops
(~µs), RPC dispatch (~µs–ms), HTTP latency (~ms), and slow recovery
operations (~s) without losing tail resolution.

If you need finer resolution, edit the `latencyBuckets` view in
`internal/observability/otel.go`. Don't crank up `count` instead — the
histogram count is the canonical `_count` series; bucket resolution is
the boundaries list.

## Useful PromQL queries

```promql
# p99 HTTP request latency by route
histogram_quantile(0.99,
  sum(rate(wire_http_request_duration_seconds_bucket[5m])) by (route, le))

# PebbleDB Set p99 — should sit at ~6 ms (fsync floor)
histogram_quantile(0.99,
  sum(rate(wire_pebble_op_duration_seconds_bucket{op="set"}[5m])) by (le))

# RPC error rate by method
sum(rate(wire_rpc_server_errors_total[5m])) by (method)
  /
sum(rate(wire_rpc_server_requests_total[5m])) by (method)

# HTTP 5xx rate (drop in)
sum(rate(wire_http_requests_total{status_class="5xx"}[5m])) by (route)

# Goroutine count (leak detector)
go_goroutines
```

## Adding a new instrument

1. Define the metric in `internal/observability/metrics.go` next to the
   existing ones — match the lazy-init pattern so subsystems can call
   the accessor before `observability.Init()` has returned.
2. Add a `record*Op` helper in the subsystem package that takes
   `op string`, `start time.Time`, and `err error`. Don't reach into
   `observability` from a hot path; cache the histogram once.
3. Document the metric name + labels in this file.
4. Verify under load — every instrument adds a few hundred ns of
   overhead per recording (hash for label set + atomic increment).
   For per-record paths in the engine, use a coarser counter (one
   `Add()` per batch, not per record).

## Future phases

- **OTel traces with OTLP exporter.** SDK is wired today (`tracer` in
  `otel.go`); needs an OTLP/HTTP or OTLP/gRPC exporter behind a
  `--traces-addr` flag, plus span instrumentation at the same four
  call sites.
- **Trace context propagation across RPC frames.** Add a
  `TraceContext` field to the RPC frame (after the existing 6-byte
  request ID) so worker → coordinator traces stitch together.
- **Engine per-record metrics.** Per-batch counter in the operator
  chain drain loop; histogram for batch size.
- **Worker metrics.** Heartbeat success/failure, task slot utilization,
  active job count.
