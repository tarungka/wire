# WIP-22 — RPC duration histogram polluted by streaming `WatchCommands`

> **Status:** deferred. Fix not yet implemented; this doc captures the
> root cause and the agreed-upon approach so we can pick it up later.

## Symptom

Under any sustained load, the **RPC p99 latency by method** panel in the
Grafana `Wire / coordinator overview` dashboard pins close to **10 s** —
specifically for the `WatchCommands` method, which then drags the
"all methods" view along with it. The 10 s figure is suspiciously round:
that's the tell.

## Root cause

`wire_rpc_server_duration_seconds` is a single histogram shared by both
unary and streaming RPCs.

1. **Stream lifetime is recorded as a request duration.**
   `internal/rpc/server.go:158-163` puts a single `defer` around the
   entire `serveStream` body, so the timer starts when the inbound frame
   is read and stops when the handler returns. For `WatchCommands` —
   the only streaming method today — the handler
   (`internal/coordinator/rpc_handlers.go` `HandleWatchCommands`)
   returns only when the worker disconnects. That's the worker's session
   lifetime: seconds to hours, never milliseconds.

2. **Histogram tops out at 10 s.**
   `internal/observability/otel.go:120-137` defines explicit bucket
   boundaries ending at `10`. Any sample > 10 s lands in the `+Inf`
   bucket. When `histogram_quantile(0.99, ...)` falls into `+Inf`, it
   returns the upper bound of the highest finite bucket — i.e. 10 s
   exactly. That's why the panel pins to 10 s rather than to the actual
   stream duration (which can be minutes or hours).

3. **Dispatch path is the same for unary and streaming.**
   `internal/rpc/server.go:158-173` runs the `defer recordRPCServerOp`
   regardless of whether the method routes through `getStreamHandler`
   (line 167) or the unary handler (line 176). No
   `kind=unary|streaming` distinction exists in the histogram labels
   either (`internal/observability/metrics.go:84-105` records only
   `method`).

Registered methods today (`internal/coordinator/transport.go:39-42`):

| Method | Kind |
|---|---|
| `RegisterWorker` | unary |
| `Heartbeat` | unary |
| `UpdateTaskStatus` | unary |
| **`WatchCommands`** | **streaming** ← polluter |

## Fix plan

**Stop recording duration for streaming RPCs**, add a `kind` attribute
to the count/error counters so they can still be filtered, and update
the dashboard query to filter `kind="unary"`.

Future streaming methods (heartbeat-stream, tail-logs, etc.) will then
inherit the right behaviour for free.

### Code changes

**`internal/rpc/server.go`**

1. Change `recordRPCServerOp` to accept a `kind string` parameter. Skip
   the histogram record when `kind == "streaming"`. Add `kind` to the
   `attribute.KeyValue` set so `count`/`errs` carry it.
2. In `serveStream`:
   - Default `kind := "unary"`.
   - When the streaming-handler branch is taken (line 167), set
     `kind = "streaming"` *before* calling `sh(...)`.
   - The existing `defer recordRPCServerOp(method, kind, dispatchStart, dispatchErr)`
     reads `kind` at handler exit, so this works without restructuring
     the defer.

**`internal/observability/metrics.go`** — no schema change needed; the
`kind` attribute is per-record, not per-instrument.

### Dashboard changes

**`examples/observability-stack/grafana/dashboards/wire.json`** — add
`{kind="unary"}` to the matcher in panels 30 / 31 / 32:

- Panel 30 (`RPC requests/sec by method`):
  `sum(rate(wire_rpc_server_requests_total{kind="unary"}[1m])) by (method)`
- Panel 31 (`RPC p99 latency by method`):
  `histogram_quantile(0.99, sum(rate(wire_rpc_server_duration_seconds_bucket{kind="unary"}[1m])) by (method, le))`
- Panel 32 (`RPC error rate by method`): same `kind="unary"` filter on
  numerator and denominator.

Old samples without the `kind` label will simply be dropped from these
queries — that's the desired behaviour.

### Verification

1. `cd examples/observability-stack && docker compose up -d --build coordinator worker prometheus grafana`
2. `RPS=20 DURATION=30s docker compose --profile k6 run --rm k6`
3. `curl http://localhost:9090/metrics | grep wire_rpc_server` — confirm
   that `WatchCommands` only shows up on `wire_rpc_server_requests_total`
   (with `kind="streaming"`), not on `wire_rpc_server_duration_seconds_*`.
4. Open Grafana `Wire / coordinator overview` → **RPC server** row.
   `RPC p99 latency by method` should show realistic values (sub-50 ms
   for unary methods) instead of pinning to 10 s.
5. `go test -race ./internal/rpc/... ./internal/coordinator/...` — no
   regressions.

## Critical files

- `internal/rpc/server.go` (lines 22-30 `recordRPCServerOp`,
  158-173 `serveStream` instrumentation)
- `internal/observability/metrics.go` (lines 84-105 `RPCInstruments`)
- `internal/observability/otel.go` (lines 120-137 latency bucket view —
  no change, just reference)
- `internal/coordinator/transport.go` (lines 39-42 — method registry,
  no change)
- `examples/observability-stack/grafana/dashboards/wire.json` (panels
  30 / 31 / 32)

## Out of scope

- Adding an `active_streams` gauge or per-frame send-duration histogram
  for streaming methods. Useful, but a follow-up — separate WIP.
- Restructuring the histogram bucket layout. The 10 s top bucket is
  fine for unary RPCs; once streaming is excluded, samples will sit
  comfortably in the lower buckets.

## Workaround until the fix lands

If you need a clean p99 number from the Grafana panel right now, edit
panel 31's query inline to `method!="WatchCommands"`. This is the
dashboard-only stopgap; it's not committed because future streaming
methods would each need to be added to the exclusion list.
