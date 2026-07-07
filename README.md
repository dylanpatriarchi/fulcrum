# Fulcrum

> A concurrent, health-aware HTTP reverse proxy and load balancer in Go.

Fulcrum is an open-source reverse proxy built on `net/http/httputil` that goes
beyond naive round-robin: pluggable balancing strategies, active **and** passive
health checking with hysteresis, bounded retry/failover, graceful draining, and
first-class observability (Prometheus + structured logs).

Everything is driven by a YAML config — no hardcoded backends or strategy.

## Status

Built in 8 milestones; each is runnable, race-free (`go test -race`), and merged
via a reviewed PR.

- [x] **M1** — Scaffold, YAML config + validation, single-backend reverse proxy
- [x] **M2** — Pool + `Strategy` interface + round-robin (distribution test)
- [x] **M3** — Weighted, least-connections (atomic), random, ip-hash
- [x] **M4** — Active health checks with a state machine + hysteresis
- [ ] **M5** — Passive health checks + bounded retry/failover
- [ ] **M6** — Timeouts, max in-flight, graceful shutdown with draining
- [ ] **M7** — Prometheus `/metrics`, structured logs, per-backend stats
- [ ] **M8** — Load-test script, Dockerfile, results & failover demo

## Quick start

```sh
make build
./bin/fulcrum -config config.example.yaml
```

## Balancing strategies

Selected by name in config (`strategy:`). The balancer registry is the single
source of truth — config validation rejects any name it does not implement.

| Name | Behaviour |
|------|-----------|
| `round-robin` | Even rotation via a lock-free atomic cursor. |
| `weighted` | Smooth weighted round-robin (nginx algorithm); exactly proportional to `weight` over a full cycle, interleaved. |
| `least-connections` | Fewest in-flight requests, read from atomic per-backend counters. |
| `random` | Uniform random pick (`math/rand/v2`, concurrency-safe). |
| `ip-hash` | Sticky sessions: a client IP is hashed (FNV-1a) to a stable backend. |

## Health checking

An active checker probes every backend on `health_check.interval` (a `GET` to
`health_check.path`, bounded by `health_check.timeout`) and flips a backend's
state only after enough **consecutive** results — `unhealthy_threshold` failures
to remove it from rotation, `healthy_threshold` successes to restore it. This
hysteresis stops a single flaky probe from flapping a backend in and out.

Unhealthy backends are excluded from the pool the strategy sees, so traffic
automatically reroutes; recovered backends re-enter on their own.

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
