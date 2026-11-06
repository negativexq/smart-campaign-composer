# Smart Campaign Composer

[![CI](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml/badge.svg)](https://github.com/negativexq/smart-campaign-composer/actions/workflows/ci.yml)

Turns a marketer's plain-language brief into a validated audience and SMS and email messages, waits for human approval, sends through a mock provider, and shows delivery status live. No real messages are ever sent.

The full path works with the mock AI or a real model: brief, validated audience, validated messages, approval, immediate or scheduled sending through the queue, live progress and a per-recipient report. `make test-e2e` covers it end to end, and `make test-chaos` kills and stops services under a running stack. The UI components are visible with sample data at http://localhost:8080/design.

To run it yourself, follow [Install from scratch](#install-from-scratch). It needs only Docker and no API key.

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
- **Rate:** each channel stays at or below 100 messages per second across all workers. A Redis sliding window gives permission before a message is claimed from the PostgreSQL queue, and each call counts until 1.1 s after its answer, so a call held up on the way cannot land with the next batch, and a step of Redis's clock of up to 100 ms lets nothing in early. If Redis loses that window, the channel waits 8 s before sending again. One channel's backlog or outage does not hold up the other.
- **No duplicates:** there is one delivery per run, recipient and channel, and every retry reuses its idempotency key. A one-time campaign has one run. Later runs of a repeating one leave out people reached before unless its schedule says to reach them again. Rejecting a repeat is then up to the provider remembering that key. The mock writes each accepted message's key to a journal on its own volume before it answers, and replays the journal when it starts, so a retry after a provider restart is recognised as the same message. Unit tests restart it between a lost answer and its retry, and the load suite restarts it in the middle of five sends.
- **Retries:** failed calls back off with jitter. A 429 pauses the channel without using up an attempt. A call with no answer may have gone through, so a message that runs out of attempts after one ends as `UNKNOWN` instead of failed.
- **Outages:** a circuit breaker holds a failing channel and tests it with a few probes, which cost messages no attempts. After 10 minutes with nothing finishing, the campaign stops as `PAUSED` with its messages intact, and a person resumes it. A channel in trouble while the campaign still moves raises an alert instead, and a 401 holds the channel without using attempts. Messages left in flight by a crashed worker are taken back through their leases.
- **Notifications:** the bell at the top right lists scheduled starts, finished sends and problems.

Details and the trade-offs behind each are in [DECISIONS.md](DECISIONS.md).

## Install from scratch

This takes a few minutes and needs no API key. It has been tested on macOS with Docker Desktop.

### 1. What you need

- [Git](https://git-scm.com/downloads)
- [Docker](https://docs.docker.com/get-started/get-docker/) with Compose 2.24 or newer, running. Check with `docker compose version`.
- Ports 8080, 8081, 8082 and 8083 free

You do not need Go, Node, PostgreSQL or Redis on your machine.

### 2. Get the code

```bash
git clone https://github.com/negativexq/smart-campaign-composer.git
```

```bash
cd smart-campaign-composer
```

### 3. Start it

```bash
docker compose up --build -d --wait
```

The command returns when every service is ready. The first run downloads the base images, so it takes longest. With those already on the machine, a full build took about a minute and starting took about 10 seconds. `make up` does the same.

On first start, setup applies migrations and loads:

- 6,000 fake customers reading Turkish, English, German, French or Spanish, with event times relative to that moment
- seven ready email templates with their images, and an example brand kit. They are ordinary templates you can edit or delete, and a deleted one stays deleted.

### 4. Check that it runs

Open http://localhost:8080. The app asks for a username or email, prefilled with `marketer@example.com`. Select **Continue**. There is no password: the name only records who did what in each campaign's activity.

You should see an empty Overview with a `Mock AI` badge at the top and Database, Rate limiter, Sending worker and Mock provider all `Up`.

| Service | URL |
|---|---|
| App | http://localhost:8080 |
| API health and metrics | http://localhost:8081/readyz, http://localhost:8081/metrics |
| Worker health and metrics | http://localhost:8082/readyz, http://localhost:8082/metrics |
| Test inbox | http://localhost:8083 |
| Mock provider health | http://localhost:8083/healthz |

### 5. Send your first campaign

1. Open **Templates**, find **Flash Sale** and select **Use template**.
2. Paste this brief, then select **Find audience**:

   ```text
   Send a 30% discount reminder to customers in Istanbul who added to their cart in the last 30 days and did not buy. Link: https://example.com/sale
   ```

3. Read the conditions and the audience count, about 570 people, then select **Continue to SMS**.
4. Check the SMS, select **Continue to email**, check the email, and select **Continue to review**.
5. On review, open each language's tab. Then select **Approve and send**. No real message is sent.
6. The campaign page shows progress live. A send this size finishes in about 20 seconds. Open its **Deliveries** tab for the report.
7. Open the test inbox at http://localhost:8083 to read the messages as customers would get them.

The test inbox shows each SMS and email the mock provider accepted, with names filled in. Opening one there sends a signed event back to the app, and the campaign's Deliveries report counts it as opened or read. The inbox is kept in the provider's journal with the messages, up to the last 5,000, so it comes back after a restart. Removing the provider's volume empties it, while the opens already reported stay in the database.

To see an audience problem, try a brief with something no filter can express, such as "customers in Antalya who are aged 25-34". The condition is named and approval is blocked.

### 6. Use a real model (optional)

The mock AI is enough to try everything. To use a real model:

```bash
cp .env.example .env
```

In `.env`, set `LLM_PROVIDER=anthropic` and `ANTHROPIC_API_KEY`, or `LLM_PROVIDER=openai` and `OPENAI_API_KEY`. `.env` is ignored by Git. Then apply it:

```bash
docker compose up -d --wait
```

The badge at the top now shows the model's name.

### 7. Stop or start over

Stop it and keep your campaigns:

```bash
docker compose down
```

Remove everything, including campaigns and uploaded images. The next start loads fresh customers and the ready templates again:

```bash
docker compose down -v
```

### Troubleshooting

| What you see | What to do |
|---|---|
| `Bind for 0.0.0.0:8080 failed: port is already allocated`, or the same for 8081 to 8083 | Stop whatever uses that port, then run the start command again. |
| `dependency failed to start: container ... api ... is unhealthy` after setting a model | A provider was chosen without its key. `docker compose logs api` says which one, such as `LLM_PROVIDER=openai requires OPENAI_API_KEY`. Fill it in `.env`, or set `LLM_PROVIDER=mock`. |
| `Cannot connect to the Docker daemon` | Start Docker and try again. |
| `Resolve the audience problems before writing messages.`, or an audience of 0 | The brief needs at least one condition the data can check: city, purchases or cart activity. "All customers" and spending or membership conditions are not filters here. |
| The email has no button | The AI only uses a link that is in the brief, and never makes one up. Add `Link: https://...` to the brief. |
| A service will not start for another reason | `docker compose ps -a` shows which one, and `docker compose logs <service>` shows why. |

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

Deeper checks live in [tests/verify](tests/verify/README.md): load runs under provider faults, Redis loss and crashed workers, security probes against a running stack, and a mutation check that the tests catch broken code. They run on their own stack next to the demo, save their results for comparison, and log what they found in [FINDINGS.md](tests/verify/FINDINGS.md).

```bash
make verify-up && make verify-security && make verify-load && make verify-down
```

CI runs on every push to `main` and every pull request: formatting, vet and the Go tests with the race detector, the frontend tests, type check and build, then the acceptance and chaos tests against the full stack on the mock AI. It needs no API key. `make benchmark` and the verification suites stay manual.

## Layout

| Path | Purpose |
|---|---|
| `cmd/` | `api`, `worker`, `mock-provider` and the one-shot `setup` |
| `internal/` | Application packages |
| `migrations/` | SQL schema |
| `frontend/` | Vue 3 and TypeScript UI |
| `tests/` | Acceptance, chaos, benchmark and verification tests |
| `docs/` | [Architecture](docs/architecture.md), [flows](docs/flows.md), [errors and retries](docs/errors-and-retries.md), the [performance profile](docs/performance.md) and the images in this file |
| `ai-sessions/` | Exported AI working sessions |

See [docs/architecture.md](docs/architecture.md) for how the system is put together, [docs/flows.md](docs/flows.md) for how a campaign moves through it, [DECISIONS.md](DECISIONS.md) for the reasoning behind it, [docs/security.md](docs/security.md) for what protects it and how it was checked, and [DESIGN.md](DESIGN.md) for the Signal design language.
