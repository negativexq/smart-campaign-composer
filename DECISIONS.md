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

## LLM providers behind one application-level interface

Claude is the primary provider, as the case requires. OpenAI and a deterministic mock share one interface defined by what the application needs, not by a vendor's wire format. Validation stays in the backend, so it does not depend on the provider. Implementation is pending.
