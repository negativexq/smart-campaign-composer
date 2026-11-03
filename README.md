# Smart Campaign Composer

Turns a marketer's plain-language brief into a validated audience and SMS and email messages, waits for human approval, sends through a mock provider, and shows delivery status live. No real messages are ever sent.

> **Status: in development.** The full path works with the mock AI: brief, validated audience, validated messages, approval, immediate or scheduled sending through the queue to the mock provider, live progress and a per-recipient report. `make test-e2e` covers it end to end. Worker crashes and outages are recovered from, and `make test-chaos` exercises them against a running stack. The OpenAI adapter is still to come. The UI components are visible with sample data at http://localhost:8080/design.

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

## LLM provider

The system runs on a deterministic mock by default, and the top bar says so with a `Mock AI` badge. To use Claude, copy `.env.example` to `.env`, then set `LLM_PROVIDER=anthropic` and `ANTHROPIC_API_KEY`. A chosen provider without its key stops the API at startup instead of quietly using the mock. OpenAI support is planned but not available yet.

## Telemetry

| Signal | Where |
|---|---|
| Metrics | `/metrics` on the API and worker |
| Logs | JSON on stdout, with `trace_id` when a span is active |
| Traces | OTLP, when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; off otherwise |
| Business audit | `audit_events` table in PostgreSQL |

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

See [DECISIONS.md](DECISIONS.md) for the reasoning behind the architecture and [DESIGN.md](DESIGN.md) for the Signal design language.
