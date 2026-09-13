# HTTP connector validation — 2026-09-12

The connector package reached **95.2% statement coverage** with the race detector
on Darwin arm64 / Go 1.25.0. This exceeds the proposal's 90% target for the
connector package; it is not a coverage claim about the entire SDK or cluster
recovery implementation.

```sh
go test -race -coverprofile=coverage.out ./sdk/connectors/httpapi
go tool cover -func=coverage.out
```

Tests exercise configuration rejection, worker factory decoding, source
lifecycle/offset failures, sequence overflow, authentication/TLS, request framing
and limits, retry exhaustion, exact idempotency IDs, and cancellation, in addition
to the initial happy-path and end-to-end SDK tests.

## Concurrent ingest and saturation

`TestSourceConcurrentBackpressure` sends 1,024 requests, each with two events,
from 32 concurrent producers into a source with capacity 128 and no reader during
the burst. Exactly 64 requests succeed and 960 receive 429. Draining returns 128
events in intact request pairs; a subsequent request succeeds. This verifies
bounded acceptance, whole-request rejection, and capacity recovery.

The reproducible saturation benchmark uses a reusable client pool bounded at 32
connections per host. On Apple M4 (10 logical CPUs), one two-second run produced:

```text
BenchmarkSourceBackpressure-10  214611  10671 ns/op  0.9994 rejected/op  8668 B/op  106 allocs/op
```

```sh
go test ./sdk/connectors/httpapi -run '^$' \
  -bench '^BenchmarkSourceBackpressure$' -benchtime=2s -count=1
```

This measures HTTP rejection overhead against a deliberately full queue, not
successful pipeline processing throughput. The initial benchmark used the
standard client's small idle pool and exhausted local ephemeral ports; that run
failed and is not a performance result. The explicit reusable pool avoids that
client-side limitation. No before/after speedup is claimed.

Cluster replay/restore integration, transactional connector behavior, automatic
runtime batching/DLQ routing, and the developer trial remain outstanding.
