# Wire — observability simulation stack

A self-contained Docker Compose stack that boots a wire coordinator + worker,
scrapes wire's `/metrics` endpoint with Prometheus, and exposes a
pre-provisioned Grafana dashboard. Use this to see real-time cluster
health and latency distributions from the OTel instrumentation.

## What you get

| Service | Port (host) | Purpose |
|---|---|---|
| `coordinator` | `4001` (HTTP API), `4002` (RPC), `9090` (metrics scrape) | Wire coordinator with `--metrics-enabled` |
| `worker` | — | Wire worker (`wire-worker-example`) registered with the coordinator |
| `prometheus` | `9091` | Scrapes wire `/metrics` every 5 s |
| `grafana` | `3000` | Pre-provisioned `Wire / coordinator overview` dashboard (anonymous viewer enabled) |
| `load` | — | Optional short-runner that submits sample jobs (profile `load`) |

## Prerequisites

- Docker Engine 20.10+ with the Compose v2 plugin (`docker compose`)
- ~1.5 GB RAM to run all four services comfortably
- Free host ports: `3000`, `4001`, `4002`, `9090`, `9091`

The wire image is built from the repo root, so the build context is `../..`.
Don't run from a different directory — `docker compose` resolves volume
and build paths relative to this `docker-compose.yml`.

## Quick start

```sh
cd examples/observability-stack

# First boot — builds the wire image (≈ 60-90 s the first time)
docker compose up --build

# In another terminal: open the dashboard
open http://localhost:3000           # macOS
xdg-open http://localhost:3000       # Linux
```

When you land on Grafana you should see the `Wire` folder in the sidebar
with a `coordinator overview` dashboard. Anonymous viewer is enabled —
no login needed to read. To edit, log in as `admin` / `admin`.

If panels are empty, wait ~10 s for the first scrape, then drive some
traffic:

```sh
# From this directory, on the host
./load.sh --rps 10 --duration 2m

# Or, in-cluster (uses the wire image already built)
docker compose --profile load up load
```

## What the dashboard shows

Four rows correspond to the four instrumented subsystems:

1. **HTTP API** — request rate by route, p50/p99 latency, status-class
   distribution, total request count.
2. **PebbleDB metadata store** — op rate by op (`get`/`set`/...),
   p99 latency on a log scale (the fsync floor at ~6 ms is
   immediately visible), errors per op.
3. **RPC server** — request rate and p99 latency per RPC method
   (`Heartbeat`, `SubmitJob`, ...), error rate as a percentage.
4. **Process** — goroutine count, RSS, CPU.

The dashboard auto-refreshes every 5 s and defaults to a 15-minute window.

## Useful URLs while the stack is running

- `http://localhost:4001/healthz` — coordinator liveness
- `http://localhost:4001/api/v1/jobs` — list jobs (after the load profile runs)
- `http://localhost:4001/api/v1/cluster/leader` — leader info
- `http://localhost:9090/metrics` — wire's raw Prometheus exposition
- `http://localhost:9091/targets` — Prometheus scrape targets (should show 1/1 up)
- `http://localhost:3000/explore` — Grafana ad-hoc PromQL

## Submitting a real job

The `load` profile runs `submit-uppercase-job` against the coordinator
five times. To submit ad-hoc:

```sh
docker compose run --rm load
```

Or from the host (rebuild the SDK example locally):

```sh
go run ../../examples/submit-uppercase-job \
  --coordinator-url http://localhost:4001 \
  --message hello --message wire --message obs
```

You should see `wire_rpc_server_requests_total{method="SubmitJob"}` tick
up in the dashboard, and the corresponding job appear in
`GET /api/v1/jobs`.

## Iterating on wire

Code changes to wire require a rebuild of the `wire` image:

```sh
docker compose up --build coordinator worker
```

The build context is the repo root, so any Go file changes are picked up.
The build cache reuses unchanged layers, so subsequent rebuilds are ~5 s
on a warm machine.

## Tearing down

```sh
docker compose down              # stop services, keep volumes (Pebble data, Grafana state)
docker compose down -v           # drop volumes too — useful between runs
```

## Architecture notes

- **Two HTTP ports on the coordinator on purpose.** The API port (`:4001`)
  serves jobs/cluster/savepoints. The metrics port (`:9090`) serves only
  `/metrics`. They run on independent listeners so scrape traffic never
  competes with API traffic, and so `/metrics` can be locked down at the
  network layer without touching the API.
- **Prometheus is on `9091` (host)** — `9090` is reserved for wire's
  scrape endpoint to avoid two services fighting over the same host
  port. Inside the compose network Prometheus is still on `9090`
  (the standard).
- **The worker doesn't expose `/metrics` yet.** The `wire-worker-example`
  binary doesn't call `observability.Init` — adding it is one of the
  next items in `docs/observability.md` under "Future phases". When
  that lands, uncomment the `wire-worker` scrape job in
  `prometheus.yml`.
- **The dashboard JSON pins `datasource.uid = PBFA97CFB590B2093`,**
  which is the well-known Grafana default UID for the first Prometheus
  datasource installed by provisioning. If you change the datasource
  name or add a second one, update the panel UIDs.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `Cannot start service ...: port is already allocated` | Some other process is using `4001`/`4002`/`9090`/`9091`/`3000`. Either stop it or change the host-side port mapping in `docker-compose.yml`. |
| `prometheus.yml: error loading config` | YAML mounted read-only; check the file is well-formed (`yamllint prometheus.yml`). |
| Dashboard shows "No data" forever | The coordinator probably isn't actually reachable. `curl http://localhost:9090/metrics | head` from the host. If that fails, `docker compose logs coordinator`. |
| `wire image is corrupt or has been altered` | Forced rebuild: `docker compose build --no-cache coordinator`. |
| Worker can't reach coordinator | The compose network resolves `coordinator` by service name, but only after `coordinator` is healthy. The `depends_on` block enforces this; if your Docker version doesn't honor health-aware deps, upgrade Compose to v2.20+. |
