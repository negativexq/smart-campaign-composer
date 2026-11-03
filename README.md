# Smart Campaign Composer

Turns a marketer's plain-language brief into a validated audience and SMS and email messages, waits for human approval, sends through a mock provider, and shows delivery status live. No real messages are ever sent.

> **Status: in development.** The services, schema, seed and test environment work. The campaign flow is not built yet, so `make test-e2e` fails on purpose until it is.

## Run

Docker is the only requirement.

```bash
docker compose up --build
```

| Service | URL |
|---|---|
| App | http://localhost:8080 |
| API health and metrics | http://localhost:8081/readyz, http://localhost:8081/metrics |
| Worker health and metrics | http://localhost:8082/readyz, http://localhost:8082/metrics |
| Mock provider health | http://localhost:8083/healthz |

Setup applies migrations and loads 5,000 fake users on first start. Event times are relative to that moment. To reseed, remove the volume with `docker compose down -v`.

## Test

```bash
make test        # Go and frontend tests in containers, no API key needed
make test-local  # Go tests with the local toolchain, for faster iteration
make test-e2e    # acceptance test against a stack started with `make up`
```

Integration tests use real PostgreSQL and fail, rather than skip, when it is missing.

## Layout

| Path | Purpose |
|---|---|
| `cmd/` | `api`, `worker`, `mock-provider` and the one-shot `setup` |
| `internal/` | Application packages |
| `migrations/` | SQL schema |
| `frontend/` | Vue 3 and TypeScript UI |
| `tests/e2e/` | Acceptance test |
| `ai-sessions/` | Exported AI working sessions |

See [DECISIONS.md](DECISIONS.md) for the reasoning behind the design.
