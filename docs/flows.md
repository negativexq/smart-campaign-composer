# Flows

How a campaign moves through the system, from a brief to a report. For the parts these flows run on, see [architecture.md](architecture.md).

## The whole journey

```mermaid
flowchart TD
    brief[Marketer writes a brief<br/>and picks channels] --> audience[AI proposes an audience]
    audience --> check{Marketer checks<br/>the audience}
    check -->|Refine or edit the brief| audience
    check -->|Looks right| writing[AI writes SMS and email<br/>for each language]
    writing --> edit{Marketer reads<br/>and edits}
    edit -->|Regenerate| writing
    edit -->|Change the audience,<br/>which clears the messages| audience
    edit -->|Approve| approved[Version and recipients are frozen]
    approved -->|Send now| queue[Deliveries are queued]
    approved -->|Schedule| scheduled[Waits for its time]
    scheduled --> queue
    queue --> worker[Worker sends within the rate limit]
    worker --> provider[Provider accepts or refuses]
    provider --> report[Live progress and report]
```

A person acts at every decision above. The model only proposes, and nothing reaches a customer until someone approves it.

## Campaign status

```mermaid
stateDiagram-v2
    [*] --> DRAFT: created
    DRAFT --> GENERATING: write messages
    READY_FOR_REVIEW --> GENERATING: regenerate
    GENERATION_FAILED --> GENERATING: try again
    GENERATING --> READY_FOR_REVIEW: written, or failed or stopped with earlier messages
    GENERATING --> GENERATION_FAILED: failed with no messages
    GENERATING --> DRAFT: stopped with no messages
    READY_FOR_REVIEW --> DRAFT: new audience
    GENERATION_FAILED --> DRAFT: new audience
    READY_FOR_REVIEW --> APPROVED: approve
    APPROVED --> DISPATCHING: send now
    APPROVED --> SCHEDULED: schedule
    SCHEDULED --> APPROVED: cancel before any run
    SCHEDULED --> DISPATCHING: run is due
    DISPATCHING --> SCHEDULED: run finished, more to come
    DISPATCHING --> PAUSED: 10 minutes with no progress
    PAUSED --> DISPATCHING: resume
    DISPATCHING --> COMPLETED: all accepted or skipped
    DISPATCHING --> COMPLETED_WITH_FAILURES: some failed or unknown
    COMPLETED --> [*]
    COMPLETED_WITH_FAILURES --> [*]
```

- Duplicating a campaign starts a new one in `DRAFT`, or in `READY_FOR_REVIEW` when it already has messages.
- A claim on `GENERATING` older than five minutes counts as left behind by a dead process, so a new generation can take it over.
- Leaving out every language that has messages also returns the campaign to `DRAFT`.
- A repeating run with nobody new to queue stays in `SCHEDULED`.
- Cancelling a schedule that has already run lets the campaign finish with what it has sent.

## Proposing an audience

`POST /api/campaigns/{id}/segment` answers with the saved proposal. The same path handles a first proposal, a refinement and an edited brief. A campaign started from a saved audience keeps that rule and skips the model until someone asks to change it.

```mermaid
sequenceDiagram
    actor M as Marketer
    participant A as API
    participant L as LLM provider
    participant D as PostgreSQL

    M->>A: Brief or refinement
    A->>D: Read the known cities
    loop Up to 3 attempts
        A->>L: Brief, schema and any earlier errors
        L-->>A: Raw JSON
        A->>A: ParseProposal: shape, cities and ranges
        A->>A: A refinement's rewritten brief adds no new offer or link
        A->>A: CheckAgainstBrief: no contradiction with the brief
    end
    Note over A: Still invalid after 3 attempts: an error, and nothing is stored
    A->>A: Keep only quotes really found in the brief
    A->>D: Count the audience with the compiled rule
    A->>D: Save rule, counts and audit event, status DRAFT
    A-->>M: Rule, evidence, counts and blockers
```

What the checks find goes to one of three levels:

| Finding | Result |
|---|---|
| The rule clearly contradicts the brief | Sent back to the model. If it cannot fix it, nothing is stored |
| A condition about who receives the message is not in the rule | Red: blocks generation and approval until the person resolves it |
| A reading the backend cannot confirm | Yellow: shown, and the person decides |

## Writing the messages

`POST /api/campaigns/{id}/generate` answers with a stream. What streams is a preview: only output that passes validation is stored.

```mermaid
sequenceDiagram
    actor M as Marketer
    participant A as API
    participant L as LLM provider
    participant D as PostgreSQL

    M->>A: Generate
    A->>D: Claim the campaign, status GENERATING
    A-->>M: plan: the languages to write
    loop Up to 3 attempts for the first language
        A->>L: Brief, audience and limits
        L-->>A: Text as it is written
        A-->>M: delta: text for each field
        A->>A: Validate limits, offer, links, facts and targeting
        opt Invalid
            A-->>M: reset: clear the screen
        end
    end
    A-->>M: language_done
    A-->>M: language: the others begin
    par One call per remaining language
        A->>L: What the first version says
        L-->>A: Translated version
        A->>A: Validate it on its own
        A-->>M: language_done
    end
    opt The campaign is an A/B test
        A-->>M: variant: B, and progress starts over
        Note over A,L: The same steps for version B, not streamed,<br/>and it must differ from version A
    end
    A->>D: Save everything, status READY_FOR_REVIEW, audit event
    A-->>M: done: the stored campaign
```

- **All or none:** if one language fails, nothing is saved, so an audience is never left with some messages out of date.
- **Stopping:** closing the page or pressing Stop cancels the calls and puts the campaign back as it was. That is not recorded as a failure.
- **Limits:** SMS and subject lengths are checked against the longest first name that will really be sent, per language.
- **Smaller runs:** regenerating on a step rewrites only that channel, and adding a language writes only that language.

## Approving and queuing

```mermaid
sequenceDiagram
    actor M as Marketer
    participant A as API
    participant D as PostgreSQL

    M->>A: POST /approve
    A->>D: Lock the campaign
    A->>A: No blockers, and all languages and versions are complete and agree on the offer
    A->>D: Store the recipients and audience counts
    A->>D: Set approved_at, which the trigger then protects
    A->>D: Status APPROVED, audit event
    A-->>M: Approved

    M->>A: POST /dispatch
    A->>D: Lock the campaign
    A->>D: One delivery per person and channel,<br/>QUEUED with consent, otherwise SKIPPED
    A->>D: Status DISPATCHING, audit event
    A-->>M: Sending started
```

The approval page stays closed until each language version has been opened. Repeating either call changes nothing. In an A/B test, each person's side comes from a hash of the campaign and the person, so a retry or a later run never moves them. `POST /schedule` replaces `/dispatch` when the send should wait: see [Scheduling](#scheduling).

## Sending a message

Each worker runs one loop per channel, with up to 32 calls in flight, so one channel's outage does not hold up the other. Permission comes before a claim, so a channel that has to wait holds no messages.

```mermaid
sequenceDiagram
    participant W as Worker channel loop
    participant B as Circuit breaker
    participant R as Redis
    participant D as PostgreSQL
    participant P as Provider

    W->>B: How many calls may I make?
    B-->>W: Allowed, or wait
    W->>R: Reserve permits
    R-->>W: Granted, or wait
    W->>D: Claim with FOR UPDATE SKIP LOCKED
    Note over W,D: Status SENDING, with a new claim_token<br/>and a one-minute lease
    W->>R: Settle: return unused permits, count the rest from now
    par For each claimed message
        W->>W: Consent is still given? Otherwise SKIPPED
        W->>W: Render the message for this person's language and version
        W->>P: POST /v1/messages with Idempotency-Key
        P-->>W: Response, or no answer
        W->>D: Attempt row and new status, in one transaction,<br/>only if claim_token still matches
    end
```

What the provider's answer leads to:

| Answer | Next status | Uses an attempt |
|---|---|---|
| 2xx | `SENT` | Yes |
| 429 | `RETRYING` after `Retry-After`, and the whole channel pauses | No |
| Other 4xx | `FAILED` | Yes |
| 5xx or 408 | `RETRYING` with backoff, up to 5 attempts | Yes |
| No answer in time | `RETRYING` with backoff, up to 5 attempts | Yes |
| Provider cannot be reached | `RETRYING`, and the channel rests for 5 seconds | No |
| Failed probe while the breaker tests the provider | `RETRYING` with backoff | No |

When attempts run out, a delivery with any timeout behind it ends as `UNKNOWN`, because the provider may have accepted the message. Otherwise it ends as `FAILED`.

## Delivery status

```mermaid
stateDiagram-v2
    [*] --> QUEUED: has consent
    [*] --> SKIPPED: no consent
    SKIPPED --> QUEUED: consent given, later run
    QUEUED --> SENDING: claimed
    RETRYING --> SENDING: claimed when due
    SENDING --> SENT: accepted
    SENDING --> RETRYING: try again
    SENDING --> RETRYING: lease expired, attempts left
    SENDING --> FAILED: refused, or attempts ran out
    SENDING --> UNKNOWN: attempts ran out after a timeout
    SENDING --> SKIPPED: consent withdrawn
    SENT --> [*]
    FAILED --> [*]
    UNKNOWN --> [*]
```

`SENT` means the provider accepted the message, not that a person received it. What became of it afterwards is recorded separately: see [Provider reports](#provider-reports).

## Keeping the queue moving

Each worker also runs these on a timer, alongside its channel loops:

```mermaid
flowchart LR
    tick1[Every second] --> recover[Take back expired leases]
    tick1 --> finish[Close campaigns with nothing open]
    tick1 --> depth[Report queue depth]
    tick1 --> schedule[Start due schedules]
    tick30[Every 30 seconds] --> stalled[Pause campaigns with<br/>10 minutes of no progress]
```

- **Worker crash:** a message left in `SENDING` is taken back when its lease ends, and this counts as an attempt with the outcome `timeout`. It is sent again with the same idempotency key, or ends as `UNKNOWN` if attempts have run out.
- **Provider failing:** when at least 20 calls in 10 seconds include 50% failures, the breaker holds the channel. It then sends three probes after 5 seconds, doubling up to 30, and messages stay queued in the meantime.
- **Redis down:** nothing is sent until it returns, and no attempt is used.
- **Postgres down:** workers wait and carry on when it returns.
- **Long outage:** a campaign with messages waiting on retries and none finishing for 10 minutes becomes `PAUSED`. Its messages keep their place, and only a person's Resume starts it again.

`make test-chaos` runs these failures against a running stack.

## Scheduling

A schedule is a row in `campaign_schedules`, including its next run time, so it survives restarts. The worker checks for due rows every second.

```mermaid
flowchart TD
    due[next_run_at has passed] --> lock[Lock the schedule and campaign]
    lock --> run[Open a run]
    run --> kind{One-time or repeating?}
    kind -->|One-time| kept[Use the recipients kept at approval]
    kind -->|Repeating| match[Find who matches now]
    kept --> insert[Add deliveries]
    match --> insert
    insert --> unique[Leave out anyone<br/>an earlier run reached]
    unique --> next[Work out the next run]
    next --> status{Anyone new, or<br/>was this the last run?}
    status -->|Yes| dispatching[Status DISPATCHING]
    status -->|No| stay[Stays SCHEDULED]
```

Missed runs are not replayed one by one: after downtime, one run covers them, because each run already reaches everyone still owed the message. A run's counts are kept once its last delivery finishes.

## Provider reports

A delivery's status only comes from our own calls. What the provider says later is stored separately in `delivery_events`.

```mermaid
sequenceDiagram
    participant W as Worker
    participant P as Mock provider
    actor T as Tester
    participant A as API
    participant D as PostgreSQL

    W->>P: POST /v1/messages
    P-->>W: Accepted
    Note over P: A few seconds later, simulated
    P->>A: POST /api/provider/events, delivered or bounced,<br/>signed with HMAC-SHA256
    A->>D: Find the delivery by idempotency key, keep the first report
    T->>P: Opens the message in the test inbox on port 8083
    P->>A: POST /api/provider/events, opened
    A->>D: Keep the first report
```

Delivered and bounced are simulated, and the screens say so. Opens are counted only when someone really opens a message in the test inbox.

## Live progress

```mermaid
sequenceDiagram
    participant B as Campaign page
    participant A as API
    participant D as PostgreSQL

    B->>A: GET /api/campaigns/{id}/events
    A->>D: Read status and counts
    A-->>B: update
    loop Every second while the campaign can change
        A->>D: Read status and counts
        alt Something changed
            A-->>B: update
        else Nothing changed for 15 seconds
            A-->>B: keep-alive comment
        end
    end
```

Workers can run anywhere, so the database is the one place that knows the truth, and a page that reconnects simply gets the current state.

## Unsubscribing

```mermaid
sequenceDiagram
    actor C as Customer
    participant A as API
    participant D as PostgreSQL
    participant W as Worker

    C->>A: GET /api/unsubscribe/{token}, from the email footer
    A->>A: Verify the HMAC signature
    A-->>C: A page that asks, because mail scanners open links
    C->>A: POST /api/unsubscribe/{token}
    A->>D: Withdraw email consent
    A-->>C: Confirmed
    Note over W,D: Consent is read again at each claim,<br/>so a queued email ends as SKIPPED
```

SMS consent does not change.

## Starting up

```mermaid
flowchart LR
    pg[postgres healthy] --> setup[setup]
    setup --> migrate[Apply migrations]
    migrate --> seed[Seed users,<br/>if the table is empty]
    seed --> starter[Load starter templates,<br/>once in an installation's life]
    starter --> done[setup exits]
    done --> api[api]
    done --> worker[worker]
    redis[redis healthy] --> worker
    provider[mock-provider healthy] --> worker
    api --> frontend[frontend]
```
