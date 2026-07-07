# Fulcrum

> A concurrent, health-aware HTTP reverse proxy and load balancer in Go.

Fulcrum is an open-source reverse proxy built on `net/http/httputil` that goes
beyond naive round-robin: pluggable balancing strategies, active **and** passive
health checking with hysteresis, bounded retry/failover, per-request timeouts,
graceful draining, and first-class observability (Prometheus + structured logs).

Everything is driven by a YAML config — no hardcoded backends or strategy — and
the whole thing is race-free, proven under `go test -race`.

## Features

- **Reverse proxy** over `net/http/httputil.ReverseProxy` to a pool of backends.
- **Pluggable strategies**: round-robin, weighted, least-connections, random, ip-hash.
- **Active health checks** with a hysteresis state machine (N consecutive results to flip).
- **Passive health checks**: a backend that keeps failing real requests is removed.
- **Bounded retry/failover** on another healthy backend, idempotency-aware.
- **Per-request timeout** and an optional **per-backend in-flight cap**.
- **Graceful shutdown** that drains in-flight requests.
- **Observability**: Prometheus `/metrics`, structured `slog` logs, per-backend `/stats`.

## Status — complete

Built incrementally; each step is runnable, race-free, and merged via a reviewed PR.

- [x] Scaffold, YAML config + validation, reverse proxy
- [x] Pool + `Strategy` interface + round-robin (distribution test)
- [x] Weighted, least-connections (atomic), random, ip-hash
- [x] Active health checks with a state machine + hysteresis
- [x] Passive health checks + bounded retry/failover (failover test)
- [x] Per-request timeout, max in-flight, graceful shutdown with draining
- [x] Prometheus `/metrics`, structured logs, per-backend stats
- [x] Load-test + failover demo scripts, Dockerfile, results

## Quick start

```sh
make build
./bin/fulcrum -config config.example.yaml
```

Or with Docker:

```sh
make docker
docker run -p 8080:8080 -p 9090:9090 fulcrum:latest
```

## Architecture

```
            ┌─────────── Proxy (retry/failover, timeout, shed) ───────────┐
request ──▶ │  Strategy.Next(healthy candidates) ──▶ Backend ──▶ upstream │ ──▶ response
            └──────────────┬───────────────────────────┬──────────────────┘
                           │ reads healthy snapshot     │ marks down on repeated errors
                     ┌─────▼─────┐                 ┌─────▼───────────┐
                     │   Pool    │◀────────────────│  HealthChecker  │ (active probes, hysteresis)
                     └───────────┘  SetHealthy     └─────────────────┘
```

- **`Backend`** — URL + weight with atomic health flag, active-conn counter and stats.
- **`Pool`** — owns backends, hands the strategy a lock-free cached snapshot of the healthy subset.
- **`Strategy`** — `Next(r, candidates) (*Backend, error)`; reserves the chosen backend so counts stay consistent.
- **`HealthChecker`** — one goroutine, probes on an interval, flips state with hysteresis.
- **`Proxy`** — wraps `ReverseProxy`, injects the strategy, handles retry/failover, timeouts and shedding.

## Balancing strategies

Selected by name in config (`strategy:`). The balancer registry is the single
source of truth — config validation rejects any name it does not implement.

| Name | Behaviour |
|------|-----------|
| `round-robin` | Even rotation via a lock-free atomic cursor. |
| `weighted` | Smooth weighted round-robin (nginx algorithm); exactly proportional to `weight` over a full cycle, interleaved. |
| `least-connections` | Fewest in-flight requests (atomic counters); select+reserve serialised so a burst can't pile onto one backend. |
| `random` | Uniform random pick (`math/rand/v2`, concurrency-safe). |
| `ip-hash` | Sticky sessions: a client IP is hashed (FNV-1a) to a stable backend. Set `proxy.trust_forwarded_headers` to hash `X-Forwarded-For` behind a trusted proxy. |

## Health checking

**Active**: a checker probes each backend on `health_check.interval` (a `GET` to
`health_check.path`, bounded by `health_check.timeout`) and flips state only
after enough **consecutive** results — `unhealthy_threshold` failures to remove
it, `healthy_threshold` successes to restore it. This hysteresis stops one flaky
probe from flapping a backend.

**Passive**: when live requests to a backend keep failing, after
`proxy.passive_max_fails` consecutive failures it is marked down immediately
(the active checker restores it once it recovers).

Unhealthy backends are excluded from the pool the strategy sees, so traffic
reroutes automatically.

## Retry / failover

If the chosen upstream fails **before any bytes reach the client** — a connection
error or a `502/503/504` gateway status — Fulcrum retries on another healthy
backend, up to `proxy.max_retries` times. Only idempotent methods are retried by
default (`proxy.retry_non_idempotent` lifts that); request bodies up to 1 MiB are
buffered so they can be replayed. Once the client has received bytes, a mid-stream
upstream failure cannot be masked — that response is not retried.

## Observability

With `admin.listen` set (default `:9090`):

- `GET /metrics` — Prometheus metrics: `fulcrum_requests_total`,
  `fulcrum_request_duration_seconds`, `fulcrum_retries_total`,
  `fulcrum_failovers_total`, `fulcrum_backend_up`, `fulcrum_backend_active_connections`.
- `GET /healthz` — liveness (`ok`).
- `GET /stats` — JSON per-backend snapshot (health, active conns, totals).

Logs are structured JSON via `slog` (backend up/down transitions, failovers,
shutdown); access logs are emitted at debug level.

## Benchmarks & failover demo

Two scripts spin up three local backends and drive load through Fulcrum:

```sh
scripts/loadtest.sh 100 10s     # throughput / latency
scripts/failover-demo.sh        # kills a backend mid-run, shows failover
```

**Throughput** (Apple M-series, 3 local Python backends, round-robin, c=100):

```
requests:     ~121k in 8s
throughput:   ~15,000 req/s
latency p50:  ~0.95 ms
latency p90:  ~1.5 ms
status codes: 100% 200 (no 5xx from the proxy)
```

**Live failover** — 8s of load at c=50, backend killed at t=3s:

```
requests:     ~111k
status codes: 200=111,295   (zero 5xx leaked to clients)
```

Only a handful of in-flight requests are interrupted at the exact instant of the
crash (mid-stream, unretryable); every other request is transparently rerouted to
a healthy backend. (Numbers are indicative; the backends are toy Python servers.)

## Configuration

See [`config.example.yaml`](config.example.yaml) for the full, documented schema.

## Development

```sh
make test-race   # race detector + all tests
make vet         # go vet
make lint        # golangci-lint (optional locally)
make cover       # coverage summary
```

## License

[MIT](LICENSE) © 2026 Dylan Patriarchi
