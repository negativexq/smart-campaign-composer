# Smart Campaign Composer

[![CI](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml/badge.svg)](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml)

Turns a marketer's plain-language brief into a validated audience and SMS and email messages, waits for human approval, sends through a mock provider, and shows delivery status live. No real messages are ever sent.

The full path works with the mock AI or a real model: brief, validated audience, validated messages, approval, immediate or scheduled sending through the queue, live progress and a per-recipient report. `make test-e2e` covers it end to end, and `make test-chaos` kills and stops services under a running stack. The UI components are visible with sample data at http://localhost:8080/design.

The screenshots show real runs of the app with an OpenAI model and the seeded fake customers. Select one to open it at full size.

### Check who will receive it

The brief becomes conditions the backend checks and counts itself. Anything no filter can express, here "aged 25-34", is named and blocks approval.

![Audience step: a brief with an unsupported age condition, the audience funnel and the approval blocker](docs/screenshots/audience.png)

### Approve what goes out

Review shows the final audience, each language's messages and a last set of checks. After approval the rules and messages cannot change.

![Review step: audience, Turkish and English messages, send timing and the checks before approval](docs/screenshots/review.png)

### Follow every message

The report follows each message from plan to arrival, separates provider acceptance from simulated delivery, and explains what did not go out.

![Deliveries report: results by channel, provider calls per second and failure reasons](docs/screenshots/delivery-report.png)

### See the whole workspace

![Overview: campaign counts, recent campaigns, activity and system health](docs/screenshots/overview.png)

## How sending behaves

- **Rate:** each channel stays at or below 100 messages per second across all workers. A Redis sliding window gives permission before a message is claimed from the PostgreSQL queue, and each send counts from when it goes out. One channel's backlog or outage does not hold up the other.
- **No duplicates:** there is one delivery per campaign, recipient and channel, and every retry reuses its idempotency key. Rejecting a repeat is then up to the provider remembering that key. The mock keeps its keys in memory, so a provider restart between a lost response and its retry could accept a message twice. The chaos tests do not cover that case.
- **Retries:** failed calls back off with jitter. A 429 pauses the channel without using up an attempt. A call with no answer may have gone through, so a message that runs out of attempts after one ends as `UNKNOWN` instead of failed.
- **Outages:** a circuit breaker holds a failing channel and tests it with a few probes, which cost messages no attempts. Messages left in flight by a crashed worker are taken back through their leases.

Details and the trade-offs behind each are in [DECISIONS.md](DECISIONS.md).

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
| Test inbox | http://localhost:8083 |
| Mock provider health | http://localhost:8083/healthz |

Setup applies migrations and loads 5,000 fake users on first start. Event times are relative to that moment. To reseed, remove the volume with `docker compose down -v`.

The test inbox shows each SMS and email the mock provider accepted, with names filled in. Opening one there sends a signed event back to the app, and the campaign's Deliveries report counts it as opened or read. The inbox is in memory, so restarting the provider empties it, while the opens already reported stay in the database.

The app asks for a username or email, prefilled with `marketer@example.com`. There is no password: the name only records who did what in each campaign's activity.

## LLM provider

The system runs on a deterministic mock by default, and the top bar says so with a `Mock AI` badge. To use a real model, copy `.env.example` to `.env`, then set `LLM_PROVIDER=anthropic` with `ANTHROPIC_API_KEY`, or `LLM_PROVIDER=openai` with `OPENAI_API_KEY`. `ANTHROPIC_MODEL` and `OPENAI_MODEL` change the model. A chosen provider without its key stops the API at startup instead of quietly using the mock.

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
make test-chaos  # kills and stops services under a running stack, takes a few minutes
make benchmark   # measures the system's limits in a single pass
```

Integration tests use real PostgreSQL and fail, rather than skip, when it is missing.

CI runs on every push to `main` and every pull request: formatting, vet and the Go tests with the race detector, the frontend tests, type check and build, then the acceptance and chaos tests against the full stack on the mock AI. It needs no API key. `make benchmark` stays manual.

## Layout

| Path | Purpose |
|---|---|
| `cmd/` | `api`, `worker`, `mock-provider` and the one-shot `setup` |
| `internal/` | Application packages |
| `migrations/` | SQL schema |
| `frontend/` | Vue 3 and TypeScript UI |
| `tests/` | Acceptance, chaos and benchmark tests |
| `docs/screenshots/` | Images in this file |
| `ai-sessions/` | Exported AI working sessions |

See [DECISIONS.md](DECISIONS.md) for the reasoning behind the architecture and [DESIGN.md](DESIGN.md) for the Signal design language.
