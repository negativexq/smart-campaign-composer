# Decisions

## PostgreSQL as the durable queue

`campaign_deliveries` is the queue, claimed with `FOR UPDATE SKIP LOCKED` and leases. Delivery state and queue state change in one transaction, which removes a whole class of dual-write bugs. Kafka or Redis Streams would add a service without solving a problem at this scale.

## Redis only for rate limiting

The 100 messages per second limit is per channel across all workers, so workers need a shared counter. Redis with an atomic Lua script provides it. If Redis is down, sending stops rather than risking the limit.

## One delivery per campaign, recipient and channel

The official requirement says the same person must not get the same message twice. The unique key is on campaign, user and channel, not on campaign version, so cancelling a campaign and approving a new version cannot message someone an earlier version already reached. Two separate campaigns may reach the same person; that is a product policy question and is out of scope.

## A separate UNKNOWN delivery status

When retries end on a timeout, the provider may have delivered the message. Recording that as `FAILED` would be false, so it gets its own `UNKNOWN` status.

## Approved versions are immutable in the database

A trigger rejects changes to approved `campaign_versions`, so the rule holds even if application code has a bug.

## Seed times are relative to first start

With fixed dates, a reviewer starting the system weeks later would find the "last 30 days" window empty. Fixture users cover the edge cases of the example brief with exact, known outcomes.

## Each process reports its own health and metrics

The worker owns Redis access and delivery metrics, so it has its own port. The API readiness check covers only PostgreSQL. Otherwise a Redis outage would mark the API unavailable even though it does not use Redis.

## Selective test-first development

Tests come first where the rules are clear and mistakes are costly: validation, state rules, idempotency, retry and failure scenarios. Handlers and UI wiring are tested alongside the code because their contracts will change. Visual design is checked by hand.

## Tests run in containers and never skip

`make test` needs only Docker. Integration tests fail when infrastructure is missing, so a green run proves the reliability tests actually ran.

## Signal, our own design language

The product follows the pattern shared by Klaviyo, Insider One and Braze: describe the goal, review a plain-language draft, then approve. It uses a guided workspace rather than a chat, because this flow has one fixed path. Mock provider acceptance is labelled `Accepted`, not delivered, because nothing confirms delivery to a person. The first version is light only, to keep scope on the core flow. See [DESIGN.md](DESIGN.md).

## LLM providers behind one application-level interface

Claude is the primary provider, as the case requires. OpenAI and a deterministic mock share one interface defined by what the application needs, not by a vendor's wire format. Validation stays in the backend, so it does not depend on the provider. A chosen provider without its key fails at startup; there is no silent fallback to the mock. The OpenAI adapter comes after the end-to-end flow works.

## Claude output is shaped by a schema but judged by the backend

Structured output asks Claude for the right shape, but it cannot enforce day ranges or the city list, and the model is still untrusted. `segment.ParseProposal` is the authority: unknown fields, unknown cities and out-of-range windows are rejected, and only a validated `Spec` can reach the compiler, which sends every value as a parameter. An empty spec compiles to `FALSE`, so a bug can never mean "everyone".

Invalid output gets up to two repair attempts, with the validation errors passed back to the model. Refusals, truncation and outages are not retried this way, because the same prompt would not fix them. Output that never validates is not stored. Conditions the filters cannot express are returned as `unsupported_conditions` and block approval, rather than being dropped or approximated.

The adapter enables Anthropic's server-side fallback, so the API can retry a refused or overloaded request on another model. `llm_model` records the configured model, so a served fallback is not visible in that column.

## The audience is counted as of the proposal time

Windows such as "last 30 days" are measured from `audience_as_of`, so the number under review does not shrink as time passes. Event data is still live: a purchase recorded after the proposal can change the count. Freezing recipients at approval is the job of the dispatch step.

## Telemetry level: production essentials, not a platform

Prometheus metrics, `slog` JSON logs, OpenTelemetry spans on critical flows, and a business audit table. Logs stay on `slog` because the Go OpenTelemetry log SDK is less mature than its tracing and metrics. Grafana, Loki, Tempo, Alertmanager and a Collector are left out on purpose: they would take time from the core flow without changing what this case demonstrates.

Metric labels use fixed values only. Validation failures carry a reason code, because the human-readable message can quote model output. Campaign IDs, user data, briefs and message text never become labels. HTTP metrics use the route pattern, so IDs and scanner traffic cannot grow the series count.

Telemetry says how the system behaves; `audit_events` says what was decided. The audit table is append-only and written in the same transaction as the change it describes, so it does not depend on telemetry retention.

## A campaign workspace, with creation as a flow inside it

The app opens on an overview rather than a form, because the system manages campaigns over time: `/` is the overview, `/campaigns` the list, `/campaigns/new` and `/campaigns/:id/edit` the creation wizard, and `/campaigns/:id` the detail page. Everything shown comes from the database, and sections without real data do not exist yet: the Content and Deliveries tabs, for example, arrive with those stages rather than as empty placeholders. For the same reason there is no Settings page and no separate Activity page.

Each status belongs to at most one list filter, so the overview's counts match what the filters show. `COMPLETED_WITH_FAILURES` sits under Failed, where someone will look for it. Campaigns have no separate name yet, so the brief serves as the title.

The Activity tab is written from `audit_events`, not from spans. There are no user accounts in this case, so entries say what happened but cannot say who did it.
