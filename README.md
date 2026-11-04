# Smart Campaign Composer

[![CI](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml/badge.svg)](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml)

Turns a marketer's plain-language brief into a validated audience and SMS and email messages, waits for human approval, sends through a mock provider, and shows delivery status live. No real messages are ever sent.

The full path works with the mock AI or a real model: brief, validated audience, validated messages, approval, immediate or scheduled sending through the queue, live progress and a per-recipient report. `make test-e2e` covers it end to end, and `make test-chaos` kills and stops services under a running stack. The UI components are visible with sample data at http://localhost:8080/design.

## Product tour

Every screenshot below is a real run: an OpenAI model wrote the audiences and messages, the seeded fake customers received them, and the mock provider accepted them. Select one to open it at full size.

### 1. Set up how your email looks

A new installation opens with seven ready templates, their images and an example brand kit. You can use them as they are, change them or delete them.

![Templates page: seven ready templates with their images, each with its version and how many campaigns use it](docs/screenshots/templates.png)

Templates are built from blocks in a three-panel editor: logo, image, heading, text, button and divider, with a live preview the backend draws for computer and phone. Select a block to change it. The AI fills the headline, message and button for each campaign; layout, images and links stay as you set them. A template stores no markup, so it cannot add a script or remove the unsubscribe link.

![Template editor: the block list, the Flash Sale preview with its headline selected, and the headline's options](docs/screenshots/template-builder.png)

Images go through one library, which redraws each upload and removes its hidden file details. The brand kit holds the logo, colors and footer new templates start from.

![Image library: the uploaded banners and logo with their sizes and whether they are in use](docs/screenshots/template-images.png)

![Brand kit: name, logo, colors, button shape, font and footer details](docs/screenshots/brand-kit.png)

### 2. Write the brief

Say who the campaign is for and what it should say, choose SMS, email or both, and start from the template you picked. A link in the brief becomes the email's button.

![New campaign: a brief, both channels selected and the Member Night template chosen](docs/screenshots/brief.png)

### 3. Check who will receive it

The brief becomes conditions the backend checks and counts itself. Anything no filter can express, here "aged 25-34", is named and blocks approval.

![Audience step: the brief with its unsupported age condition marked, the audience funnel and the approval blocker](docs/screenshots/audience.png)

The same step lists the languages the audience reads, with counts. Choosing them changes who gets their own version, never who receives the campaign: anyone whose language is left out gets English.

### 4. Check the messages

The AI writes the SMS and email in each chosen language. Every field can be edited, the template can be changed, and the preview shows the email as a named customer would see it. You can also ask for a new version in your own words.

![Email step: Turkish and English tabs, the template picker, the editable fields and a preview of the Member Night email](docs/screenshots/email-content.png)

### 5. Approve what goes out

Review shows the final audience, each language's messages, when to send and a last set of checks. A person has to approve, and review asks them to open each language first. After approval the rules and messages cannot change.

![Review step: audience, SMS and email previews, send timing and the checks before approval](docs/screenshots/review.png)

### 6. Follow the send live

Messages go through a queue at no more than 100 per second per channel. Progress updates while they go out, and rate limits and provider errors are retried. The delivery monitor shows what is waiting or stuck across every campaign.

![Campaign page during a send: 33% processed, with accepted, skipped and waiting counts for each channel](docs/screenshots/campaign-live.png)

![Delivery monitor during a send: messages by status, channel and last problem, and a list of those being retried](docs/screenshots/delivery-monitor.png)

### 7. See what happened

The report follows each message from plan to arrival, separates provider acceptance from simulated delivery, and explains what did not go out.

![Deliveries report: results by channel, provider calls over time and the reasons messages did not go out](docs/screenshots/delivery-report.png)

The test inbox shows each message the mock provider accepted, as the customer would get it: here the Winter campaigns template in Turkish, with the name filled in. Nothing leaves your machine.

![Test inbox: a list of accepted emails in several languages and one open winter sale email in Turkish](docs/screenshots/test-inbox.png)

### 8. See the whole workspace

The overview shows campaign counts, recent activity and system health. Analytics compares results across campaigns.

![Overview: campaign counts, recent campaigns and activity](docs/screenshots/overview.png)

![Analytics: messages by channel and result, provider calls by day and campaigns by failure rate](docs/screenshots/analytics.png)

## How sending behaves

- **Audience:** a one-time send goes to the people who were approved, and a repeating one finds who matches at each run. Consent is checked again either way.
- **Rate:** each channel stays at or below 100 messages per second across all workers. A Redis sliding window gives permission before a message is claimed from the PostgreSQL queue, and each send counts from when it goes out. One channel's backlog or outage does not hold up the other.
- **No duplicates:** there is one delivery per campaign, recipient and channel, and every retry reuses its idempotency key. Rejecting a repeat is then up to the provider remembering that key. The mock keeps its keys in memory, so a provider restart between a lost response and its retry could accept a message twice. The chaos tests do not cover that case.
- **Retries:** failed calls back off with jitter. A 429 pauses the channel without using up an attempt. A call with no answer may have gone through, so a message that runs out of attempts after one ends as `UNKNOWN` instead of failed.
- **Outages:** a circuit breaker holds a failing channel and tests it with a few probes, which cost messages no attempts. After 10 minutes with nothing finishing, the campaign stops as `PAUSED` with its messages intact, and a person resumes it. Messages left in flight by a crashed worker are taken back through their leases.
- **Notifications:** the bell at the top right lists scheduled starts, finished sends and problems.

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

Setup applies migrations and loads 6,000 fake users reading Turkish, English, German, French or Spanish on first start. Event times are relative to that moment. It also adds seven ready email templates with their images and an example brand kit, so the Templates page has something to use and copy; they are ordinary templates you can edit or delete, and a deleted one stays deleted. To reseed, remove the volume with `docker compose down -v`.

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
