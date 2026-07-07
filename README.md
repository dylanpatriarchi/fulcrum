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
- [ ] **M2** — Pool + `Strategy` interface + round-robin (distribution test)
- [ ] **M3** — Weighted, least-connections (atomic), random, ip-hash
- [ ] **M4** — Active health checks with a state machine + hysteresis
- [ ] **M5** — Passive health checks + bounded retry/failover
- [ ] **M6** — Timeouts, max in-flight, graceful shutdown with draining
- [ ] **M7** — Prometheus `/metrics`, structured logs, per-backend stats
- [ ] **M8** — Load-test script, Dockerfile, results & failover demo

## Quick start

```sh
make build
./bin/fulcrum -config config.example.yaml
```

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
