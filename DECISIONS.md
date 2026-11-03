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

Claude is the primary provider, as the case requires. OpenAI and a deterministic mock share one interface defined by what the application needs, not by a vendor's wire format. Validation stays in the backend, so it does not depend on the provider. A chosen provider without its key fails at startup; there is no silent fallback to the mock.

The OpenAI adapter uses the Responses API with a strict JSON schema and `store: false`, because nothing continues an earlier response and a brief has no reason to stay on OpenAI's side. Strict mode requires every property, while our segment filters are optional, so the adapter rewrites its copy of the schema: an optional property becomes required but may be null. The shared schema stays as it is, and `segment.ParseProposal` reads null the same as a missing filter. This adapter has no fallback model like the Claude one, so a refusal ends the attempt.

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

The app opens on an overview rather than a form, because the system manages campaigns over time: `/` is the overview, `/campaigns` the list, `/campaigns/new` and `/campaigns/:id/edit` the creation wizard, and `/campaigns/:id` the detail page. Everything shown comes from the database, and sections without real data do not exist yet: the Deliveries tab, for example, arrives with that stage rather than as an empty placeholder, and the Content tab only appears once a campaign has messages. For the same reason there is no Settings page and no separate Activity page.

Each status belongs to at most one list filter, so the overview's counts match what the filters show. `COMPLETED_WITH_FAILURES` sits under Failed, where someone will look for it.

A campaign starts with a provisional name cut from its brief. The proposal suggests a better one, which passes the same validation as the rest of the model's output, and anyone can rename the campaign. `name_source` records who chose the name, so a suggestion never replaces a name someone typed. Renaming works in every status, because a name labels the campaign rather than being part of the immutable approved version. It is still recorded in the audit trail.

The Activity tab is written from `audit_events`, not from spans. There are no user accounts in this case, so entries say what happened but cannot say who did it.

## Streamed content is a preview, and only the finished output counts

Messages stream to the browser field by field, because waiting in silence for a model feels broken. That text is display only. A small parser pulls the fields out of the partial JSON, but nothing is stored or marked as validated until the complete output passes `content.ParseGenerated`. If validation fails and a repair runs, or Anthropic's server-side fallback restarts the response on another model, a `reset` event clears the screen, so abandoned text cannot be mistaken for the result. On failure the browser goes back to what the backend last accepted.

Generation is tied to the request: closing the page or pressing Stop cancels the call and puts the campaign back as it was, and that is not recorded as a failure. `POST /generate` claims the campaign with a conditional update, so two generations cannot run at once. A claim older than five minutes, longer than every attempt combined, is treated as left behind by a dead process and can be taken over. Browsers' `EventSource` cannot send a POST, so the client reads the stream with `fetch`.

## Limits are checked against the longest name that will really be sent

`{{first_name}}` has a different length for everyone, so checking the template as written would let a message pass and then exceed the limit for some recipients. Limits use the longest first name among people who consented to that channel, or the fallback if it is longer. The editor counts the same way, and previews offer the longest name and a missing name alongside a typical one.

## A discount mismatch stops the model but only warns a person

The model may not invent or drop an offer: a percentage that differs from the brief fails validation and goes through repair. An instruction such as "make it 40%" becomes the source for that run, because the marketer asked for it. A marketer's own edit can also change the offer, since they are the authority on it, so it passes with a visible warning and stays visible until the brief and message agree. Length, variable and empty-field rules are hard for both, because a person editing cannot make an SMS fit by intent.

## Changing the audience clears the content

Messages are written for a specific audience, and its longest name sets the limits. Keeping them after a change would leave text checked against limits that no longer apply, so a new proposal puts the campaign back to a draft without content, and the page says so before it happens.

## A campaign chooses its channels when it is created

A campaign uses SMS, email or both, and that choice is fixed once it exists. It is stored on the campaign rather than derived from which text was written, because the choice decides what gets written: the model is asked only for the chosen fields, text for any other field is dropped whoever wrote it, and deliveries are only created on the chosen channels. Reachable counts are zeroed for an unused channel before the approval checks run, so a campaign is not blocked by missing consent on a channel it does not use, and the number on the approve button stays exact.

## One email template, filled with text

The model and the marketer write text only: subject, preview text, headline, body, button label and link. The backend puts that text into one table-based responsive template with `html/template`, so neither can add markup, and the plain text alternative comes from the same fields. Letting a model write HTML would need a sanitizer, would break differently in each mail client, and would make every campaign a new design to check. The cost is that campaigns cannot have their own layout or images.

The preview calls the same renderer through an endpoint instead of copying it in the frontend, so what a marketer checks is what is sent. It is shown in a sandboxed frame with no scripts.

A button link from the model must appear in the brief, the instruction or the current version, because a made-up address sends real people somewhere nobody chose. A person editing the message can set any full http or https link. The footer text is English even for Turkish messages, which has the same cause as the `there` fallback: campaigns have no language field yet.

## Unsubscribing takes a signed link and a button

Every email has its own link, signed with HMAC-SHA256, so it needs no session and cannot be changed to withdraw someone else's consent. Opening it only asks, because mail scanners open links; the POST withdraws email consent and can be repeated. SMS consent does not change. The worker reads consent again before each send, so queued emails for that person end as `consent_withdrawn` with no extra code. The worker refuses to start without `UNSUBSCRIBE_SECRET`, so no campaign can go out with a footer that does not work.

## Tone, discount and recent message exclusion are optional form fields

The brief can still say all of this, so the fields stay optional and a marketer who writes everything in the brief loses nothing.

A discount on the form is added to the brief that the writer and the discount check read, so it follows the same rules as one in the text. A brief naming another percentage is refused, because guessing which offer is meant could send the wrong price.

Tone goes to the model separately and changes wording only. It has no validation beyond the allowed values, since whether a text sounds formal is for the person reviewing it.

"Skip people messaged recently" is a filter in the segment rule rather than a separate step, so counts, previews, the approved version and repeat runs all apply it the same way, and it shows in the conditions. It counts `SENT` and `UNKNOWN` deliveries from any campaign before the time the audience was counted, because an `UNKNOWN` message may have arrived and a campaign's own messages must not shrink its audience. A campaign sent right after approval uses the audience counted at proposal time, so anything another campaign sends in between is not excluded; repeat runs count afresh.

## Tried against a real model

The example brief ran through the app with OpenAI's `gpt-6-luna`: the audience, streaming, validation, approval and sending all worked, and the first text arrived after about 4.5 seconds. It chose the same filters as the mock, wrote natural Turkish, followed a formal tone, used the discount from the form and left the button out when the brief had no link. It did not follow a brief telling it to forget its rules. Claude has not been tried with a real key.

That run showed one limit. Every percentage in a brief must appear in every message, so a brief naming 10% and 90% cannot pass even when the model picks sensibly. Generation then stops with nothing stored, and an instruction naming the discount settles it, while the review page keeps pointing out the difference. A brief with two real offers has to be written as separate campaigns.

## The audience rule is shown as a breakdown

The marketer checks the rule in a breakdown table: from the main audience down through each condition, with what was selected, the brief's wording for it and how many people remain. Counts apply the conditions one at a time at the audience's own timestamp. The raw JSON rule stays out of the interface because it adds nothing a marketer can act on. A discount is not a condition, so it sits with the message settings and only exists once someone adds it.

## Approval freezes the audience counts

Counts used to be recalculated whenever a page opened, so a later consent change could move the number on a finished campaign. Approval now stores the counts and the step-by-step breakdown with the version, and the Audience tab says "snapshot" only when one exists. What was really sent stays a separate figure read from the delivery records, because consent is checked again at send time.


## Each condition shows the words it came from

The model returns, for each filter, the exact words of the brief or refinement behind it. The backend keeps a quote only if it really appears in that text, ignoring case and spacing, and a filter without one is shown as added by the AI. A false quote costs only the explanation, so it does not reject the proposal. The check proves the words were written, not that the filter means the same thing, which is what human approval is for.

## Known limits of content generation

Regeneration replaces every message rather than one, which keeps the validation of the offer across channels simple. The `there` fallback for a missing name is English and reads awkwardly in a Turkish message; per-language fallbacks belong with per-language content.

## Scheduling repeats a campaign without repeating a message

A campaign can be sent now, once at a chosen time, at regular intervals, or on chosen weekdays. Marketers pick from these forms instead of writing a cron expression, which is easy to get wrong and hard to read back. A loop in the worker plays the part of the cron job, and its state lives in `campaign_schedules`, so it survives restarts.

"The same person never gets the same message twice" is a hard rule, so a repeat cannot mean sending again to everyone. Each run counts the audience again and queues only people without a delivery, and the unique key on campaign, user and channel enforces this. People who were only skipped can be queued later if they consent. A repeating campaign is therefore a way to reach people who match later, and a run can correctly queue nothing. To send new content to the same people, a marketer creates a new campaign.

Missed runs are not replayed one by one. After downtime one run covers them, because each run already reaches everyone who is still owed the message.

An immediate send counts the audience at the proposal time, so it matches the number that was approved. A scheduled send counts at run time, because its point is to reach the people who match then. The UI says the number can differ.

Known limit: a person who starts matching later can have a longer first name than anyone checked at approval. If their SMS then exceeds the limit, it ends as FAILED after a provider 400 rather than being cut.

## The rate limiter is a sliding window in Redis

A fixed one-second bucket allows up to 200 messages across a bucket boundary. A Lua script keeps a sliding window for each channel, so no one-second span goes over 100, however many workers run. If Redis is unavailable, messages wait and no attempt is used: sending without the limiter would risk breaking a hard limit.

## An earlier timeout makes the result UNKNOWN

A timeout means the provider may have accepted the message. Retrying with the same idempotency key is safe, but if the retries end in failure, that failure does not prove the first call failed. So a delivery with any earlier timeout ends as UNKNOWN unless a later call is accepted.

## A 429 does not use up an attempt

Rate limiting says nothing about the message, so counting it would turn a busy provider into failed deliveries. The worker waits for Retry-After and tries again. This is why the report can show more responses than attempts, and the UI explains it.

## Live progress reads the database and pushes over SSE

Each open campaign page has a stream that reads aggregates once a second and sends only changes. Workers can run anywhere, so the database is the one place that knows the truth, and a client that reconnects simply gets the current state. Viewers add load per connection: on a campaign of a million messages one read costs a few hundred milliseconds, so many people watching the same huge send would call for sharing one read between them.

## The report explains the deliveries instead of decorating them

Every chart on the Deliveries tab is a way into the table below it. Selecting a channel segment, a time bar, a reason or a problem filters the table to those people and says in words what is showing; selecting it again clears it. The report only uses what we measured: results come from `campaign_deliveries`, calls from `delivery_attempts`. There is no delivered status, because the mock provider only says it accepted a message. Opens are counted only when someone really opens a message in the test inbox, and we generate none: invented engagement would suggest a proof we do not have.

A problem filter looks at every call, not the last one, so a message accepted after a 429 is still found under rate limited. A time bar lists who was called in that period, which also makes the pauses after a 429 visible as gaps. Bars stop at 60 per chart, and the width grows with the send so a schedule running for weeks stays readable.

Status colors failed the palette check for color-blind readers: red and amber differ by a Delta E of 2.8. So the time chart uses blue and amber instead, and Unknown is textured in the channel bar.

On 1M deliveries and 900K calls the report's four queries took about 600 ms together on a table holding one campaign, with no new index. While sending, the page rereads the report at most every 5 seconds. We added no index for it, since each one slows sending.

## The test inbox reports opens through a real webhook

Someone testing the product needs to see what was sent, so the mock provider keeps recent accepted messages and serves a page at port 8083. Opening a message there marks it opened and posts an event to `POST /api/provider/events`, as a real provider's event webhook would. That keeps the path real and leaves the demo honest, because a count in the report is always a person's action.

The event is signed with HMAC-SHA256, since this route has no session. It finds the delivery by idempotency key rather than message ID, because a message whose response was lost has no recorded ID, and it can be repeated safely because only the first report of an event is kept. `delivery_events` is separate from `delivery_attempts`: an attempt is our call to the provider, an event is the provider telling us something later. An opened message whose outcome is UNKNOWN stays UNKNOWN, because that status records what our call returned.

The inbox and its webhook retries live in memory. Restarting the provider empties the inbox and loses events it had not yet delivered. The labels say opened for email and read for SMS and name the test inbox, because real providers report email opens unreliably and SMS has no read signal.

## One delivery table, not a table per campaign

We measured this on 3.6 million deliveries, 2.9 million attempts and 2,000 campaigns, on one machine with generated data, so the numbers show size and direction rather than a benchmark.

2,000 campaigns as separate partitions took the catalog from 477 to 10,477 relations with no real rows in them. Planning the worker's next-batch query took 340 to 415 ms against about 2 ms on the shared table, and a query across them held about 6,000 locks. The default lock table could not even create them in one transaction. A table per campaign would also move the unique rule, foreign keys and every migration into generated SQL, and every report across campaigns would need to reach thousands of tables.

The shared table stays fast because of what the work actually needs. The queue indexes are partial, so they hold only unfinished messages and do not grow with history: the sweep for expired leases took 0.014 ms beside 3.6 million rows. Campaign-level reads go through indexes that start with campaign_id, so a campaign reads only its own rows.

If history ever becomes the problem, the next step is partitioning by month with old months archived, not by campaign. Nothing we measured calls for that yet.

## Indexes and query shapes follow measured plans

The biggest gains came from rewriting queries, not adding indexes.

| Query | Before | After | Change |
| --- | --- | --- | --- |
| Claim a batch with a million waiting | 724 ms | under 1 ms | Subquery instead of a join, so the planner reads the claimable index in order |
| Provider responses for a large campaign | 2.9 s | 57 ms | `delivery_attempts.campaign_id` with an index |
| Campaign list, 100 rows | 716 ms to 3.1 s | about 100 ms | Choose the page first, then count only its campaigns |

The claim query mattered most: at 724 ms per batch of 32, the planner alone would have held sending under 100 messages a second.

We left some out on purpose, because every index on the delivery table slows sending. An index for filtering deliveries by status took 206 MB and saved about 20 ms. Including `attempts` in the progress index took 177 MB rather than 26 MB without a clear gain, since rows change constantly during a send. An index on `user_id` or `sent_at` would speed up reports that no screen has yet (126 ms and 440 ms today).

When dashboards need history across campaigns, a summary table updated when a campaign finishes will serve them better than more indexes on the queue. A filter that matches few rows late in a very large campaign can still take about 100 ms.

## Messages left in flight are taken back, and may stay unknown

A worker that crashes after claiming a message leaves that row in SENDING with a lease. The worker loop checks for expired leases every second and takes those rows back. We cannot tell whether the provider received the message, so this counts as an attempt with the outcome `timeout`. If attempts run out, the delivery ends as UNKNOWN instead of being sent again or called failed. A stale worker cannot overwrite the result, because `claim_token` no longer matches.

A 429 pauses the whole channel through a shared Redis key, so every worker honours one Retry-After. The pause only ever gets longer and is capped at 30 seconds. If the provider cannot be reached at all, nothing was sent, so no attempt is used and the channel rests for 5 seconds. If the limiter itself is unavailable, nothing is sent until it returns.

`make test-chaos` checks this on a running stack by killing the worker, stopping it, and stopping Redis, the provider and Postgres during a send. In a full run all five ended with every delivery finished and, where the test compares them, the provider's count matching our records. A message taken back after a crash is sent again with the same idempotency key, so the promise of no second message depends on the provider honouring that key, as the mock does.

## A simple sign-in that says who, not who you really are

Signing in takes a username or email and no password. The name goes into an unsigned HttpOnly cookie for 30 days, and every campaign route answers 401 without it. This gives the audit trail a name to record and is not authentication: anyone can type any name or edit the cookie, and the page says so. A real login would replace the cookie handling in `internal/httpapi/session.go` and leave the rest alone.

`audit_events` records `actor_type` and `actor`. A step taken in a request is `user` with the name, work the worker or scheduler does is `system`, and rows older than this change are `unknown`, because guessing a name would falsify the record. `campaigns.created_by` and `campaign_schedules.created_by` keep who created a campaign and who set up its schedule, so a scheduled run can read "by the system, set up by X".

## Schedules live in the database

A schedule is a row in `campaign_schedules`, including its next run time, and the scheduler keeps nothing in memory. Restarting any service or the machine loses nothing, and the data stays while the Postgres volume does: `make down` keeps it, `docker compose down -v` deletes it. Seeding skips a database that already has users. A one-time send whose time passed during downtime goes out when the system returns, and missed intervals become one run.

## A refinement rewrites the brief

The brief is what a marketer reads first and what message writing starts from, so it has to say the same thing as the rule. A refinement returns the whole brief rewritten next to the new rule, and the two are saved together.

The rewrite is model output, so it is checked like the rest. It is rejected and retried if it is empty, too long, or brings in a discount or link that neither the old brief nor the refinement contains, and a discount that contradicts the one chosen for the campaign is refused with the existing conflict error. A rewrite on a first proposal is ignored, because only a person's request can change a person's brief. The page says the brief changed and names the request behind it, and the audit trail records that it changed but not the text.

## SMS and email are separate steps

One page held seven fields, two previews and a state and origin label on every field, so the setup flow is now Brief, Audience, SMS, Email, Review, without the step of a channel the campaign does not use. Each step shows one state and one origin for its whole message, and an email's optional button stays folded until it has something to say.

The first arrival still writes all channels in one model call, because both messages come from the same brief and are validated together. Regenerating on a step rewrites only that channel, so asking for a shorter SMS cannot throw away edits made to the email. The stored row stays one document per campaign version.

Moving to another step saves what was typed, so there is no separate save button and no way to walk away from unsaved text by accident. Text the backend refuses keeps the person on the step with the problem shown on the field.
