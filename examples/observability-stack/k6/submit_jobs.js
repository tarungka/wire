// k6 script — drive job submissions against a running coordinator.
//
// Measures HTTP submit latency for POST /api/v1/jobs at a configurable
// arrival rate. Each request sends a real (Source -> Map(upper) -> Sink)
// graph so the worker actually deploys and runs the job; the worker has
// 4 task slots, so beyond ~4 concurrent in-flight jobs the scheduler
// queues remaining ones in CREATED state and dispatches as slots free.
//
// What this measures: time from k6 sending POST to receiving 201. That is
// roughly { JSON parse + name uniqueness check + 2x Pebble Set (job meta +
// config) + scheduler kick + JSON encode of response }, dominated by the
// Pebble fsync floor (~6 ms).
//
// What this does NOT measure: end-to-end dispatch latency (POST -> worker
// receives DeployTask). Watch that in the Grafana dashboard via the
// wire_rpc_* histograms while this script is running.
//
// Env knobs (all optional):
//   WIRE_API     — coordinator URL (default http://localhost:4001)
//   GRAPH_BYTES  — base64-encoded msgpack rpc.JobGraph (REQUIRED — produced
//                  by print-uppercase-graph; the docker compose stack feeds
//                  this in via the k6-graph-init shared volume)
//   RPS          — target arrival rate per second (default 20)
//   DURATION     — k6 duration string, e.g. 30s, 2m (default 30s)
//   PRE_VUS      — pre-allocated VUs (default 20)
//   MAX_VUS      — max VUs (default 200)

import http from 'k6/http';
import { check } from 'k6';

const API = __ENV.WIRE_API || 'http://localhost:4001';
const GRAPH = __ENV.GRAPH_BYTES;
if (!GRAPH) {
  throw new Error('GRAPH_BYTES env var is required (run print-uppercase-graph to produce it)');
}

export const options = {
  scenarios: {
    submit: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RPS || 20),
      timeUnit: '1s',
      duration: __ENV.DURATION || '30s',
      preAllocatedVUs: Number(__ENV.PRE_VUS || 20),
      maxVUs: Number(__ENV.MAX_VUS || 200),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    'http_req_duration{name:submit_job}': ['p(95)<500', 'p(99)<1000'],
  },
};

const payloadHeaders = { 'Content-Type': 'application/json' };

export default function () {
  const body = JSON.stringify({
    name: `loadtest-${__VU}-${__ITER}-${Date.now()}`,
    parallelism: 1,
    graph_bytes: GRAPH,
  });
  const res = http.post(`${API}/api/v1/jobs`, body, {
    headers: payloadHeaders,
    tags: { name: 'submit_job' },
  });
  check(res, {
    'status 201': (r) => r.status === 201,
  });
}
