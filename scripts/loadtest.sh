#!/usr/bin/env bash
# Load-test Fulcrum against 3 local backends and print throughput/latency.
#
# Usage: scripts/loadtest.sh [concurrency] [duration]
set -euo pipefail

cd "$(dirname "$0")/.."

CONCURRENCY="${1:-100}"
DURATION="${2:-10s}"
PORTS=(9101 9102 9103)
LISTEN="127.0.0.1:8080"

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT

echo "==> building fulcrum + loadgen"
go build -o bin/fulcrum ./cmd/fulcrum
go build -o bin/loadgen ./cmd/loadgen

echo "==> starting backends on ${PORTS[*]}"
for p in "${PORTS[@]}"; do
  python3 scripts/fake_backend.py "$p" "backend-$p" &
  pids+=($!)
done

CFG="$(mktemp)"
cat >"$CFG" <<EOF
listen: "$LISTEN"
strategy: round-robin
backends:
  - url: "http://127.0.0.1:9101"
  - url: "http://127.0.0.1:9102"
  - url: "http://127.0.0.1:9103"
health_check:
  interval: 2s
  timeout: 500ms
  path: /healthz
proxy:
  max_retries: 2
admin:
  listen: "127.0.0.1:9090"
EOF

sleep 1
echo "==> starting fulcrum"
./bin/fulcrum -config "$CFG" >/dev/null 2>&1 &
pids+=($!)
sleep 1

echo "==> load test (c=$CONCURRENCY, d=$DURATION)"
./bin/loadgen -url "http://$LISTEN/" -c "$CONCURRENCY" -d "$DURATION"

echo "==> a few metrics"
curl -s "http://127.0.0.1:9090/metrics" | grep -E '^fulcrum_requests_total' | head
