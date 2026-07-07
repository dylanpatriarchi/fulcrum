#!/usr/bin/env bash
# Demonstrate live failover: run load through Fulcrum, kill a backend mid-run,
# and show that clients keep getting 2xx (no 5xx leaks) thanks to retry/failover.
#
# Usage: scripts/failover-demo.sh
set -euo pipefail

cd "$(dirname "$0")/.."

PORTS=(9101 9102 9103)
KILL_PORT=9102
LISTEN="127.0.0.1:8080"
victim_pid=""

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
}
trap cleanup EXIT

echo "==> building"
go build -o bin/fulcrum ./cmd/fulcrum
go build -o bin/loadgen ./cmd/loadgen

echo "==> starting 3 backends"
for p in "${PORTS[@]}"; do
  python3 scripts/fake_backend.py "$p" "backend-$p" &
  pid=$!
  pids+=($pid)
  if [ "$p" = "$KILL_PORT" ]; then victim_pid=$pid; fi
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
  interval: 1s
  timeout: 500ms
  path: /healthz
  unhealthy_threshold: 1
proxy:
  max_retries: 2
  passive_max_fails: 1
admin:
  listen: "127.0.0.1:9090"
EOF

sleep 1
echo "==> starting fulcrum"
./bin/fulcrum -config "$CFG" >/dev/null 2>&1 &
pids+=($!)
sleep 1

echo "==> starting 8s load; will kill backend 9102 after 3s"
./bin/loadgen -url "http://$LISTEN/" -c 50 -d 8s &
load_pid=$!

sleep 3
echo "==> KILLING backend $KILL_PORT (simulating a crash)"
kill "$victim_pid" 2>/dev/null || true

wait "$load_pid"

echo "==> failover/retry metrics"
curl -s "http://127.0.0.1:9090/metrics" | grep -E '^fulcrum_(retries_total|failovers_total|backend_up)' || true
echo
echo "==> per-backend stats"
curl -s "http://127.0.0.1:9090/stats"
echo
echo "If 'errors: 0' above, no client saw a failure despite the mid-run crash."
