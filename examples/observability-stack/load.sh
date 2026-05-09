#!/usr/bin/env bash
#
# load.sh — drive a steady trickle of HTTP traffic against a running
# observability-stack so the Grafana dashboards have data.
#
# Usage:
#   ./load.sh                              # run forever, ~5 req/s
#   ./load.sh --duration 60s               # stop after 60 s
#   ./load.sh --rps 20                     # 20 req/s
#
# Requires the stack to be up (`docker compose up`) — talks to
# http://localhost:4001.

set -euo pipefail

API="${WIRE_API:-http://localhost:4001}"
DURATION=""
RPS=5

while [[ $# -gt 0 ]]; do
  case "$1" in
    --duration) DURATION="$2"; shift 2 ;;
    --rps)      RPS="$2"; shift 2 ;;
    --api)      API="$2"; shift 2 ;;
    --help|-h)
      grep '^#' "$0" | sed 's/^# \?//'
      exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

interval=$(awk -v r="$RPS" 'BEGIN { print 1/r }')

end_at=""
if [[ -n "$DURATION" ]]; then
  end_at=$(date -d "+$DURATION" +%s 2>/dev/null || gdate -d "+$DURATION" +%s)
fi

echo "driving $RPS req/s against $API (interval ${interval}s)"
[[ -n "$end_at" ]] && echo "until $(date -d "@$end_at" 2>/dev/null || gdate -d "@$end_at")"

i=0
while true; do
  i=$((i + 1))
  # Mix of 200 and 404 routes so the dashboard's status_class panel isn't flat.
  case $((i % 5)) in
    0) curl -s -o /dev/null "$API/healthz" ;;
    1) curl -s -o /dev/null "$API/readyz" ;;
    2) curl -s -o /dev/null "$API/api/v1/jobs" ;;
    3) curl -s -o /dev/null "$API/api/v1/cluster" ;;
    4) curl -s -o /dev/null "$API/api/v1/jobs/does-not-exist" ;;
  esac
  if [[ -n "$end_at" ]] && [[ "$(date +%s)" -ge "$end_at" ]]; then
    echo "stopping after $i requests"
    break
  fi
  sleep "$interval"
done
