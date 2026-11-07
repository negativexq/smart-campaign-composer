# Decisions

## PostgreSQL as the durable queue

`campaign_deliveries` is the queue, claimed with `FOR UPDATE SKIP LOCKED` and leases. Delivery state and queue state change in one transaction, which removes a whole class of dual-write bugs. Kafka or Redis Streams would add a service without solving a problem at this scale.

## Redis only for rate limiting

The 100 messages per second limit is per channel across all workers, so workers need a shared counter. Redis with an atomic Lua script provides it. If Redis is down, sending stops rather than risking the limit.

Redis also keeps the state that goes with the limit and must be shared by every worker: a channel paused by a 429, a channel locked by refused credentials with the time of its next probe, and the key that marks each window whole. All of it describes the last seconds or minutes, so Redis runs with no snapshots and starts empty after a restart; losing it is what the limiter's 8 s hold covers (see the rate limiter section).

## One delivery per run, recipient and channel

The official requirement says the same person must not get the same message twice. Each send of a campaign is a run: the single dispatch of a one-time campaign, or one firing of a repeating schedule. The unique key is on run, user and channel, and every delivery's idempotency key names its run, so a crash, a retry, a lost response or a run picked up again cannot message anyone twice within it. A later run can only reach someone again when its schedule says so (see the scheduling section). Two separate campaigns may reach the same person; the "not messaged in the last N days" rule is how a campaign holds back people any campaign reached recently.

A finished run keeps its counts in `campaign_runs`, written in the same statement that marks it finished. A run gets all its deliveries when it opens and a finished delivery does not change, so those counts are final, and lists and progress read them instead of counting every delivery a campaign ever made. Only runs still sending are counted live. Existing deliveries became each campaign's first run. A campaign that repeats gets a Runs tab listing each run with its own counts; opening one scopes the overview's progress, the deliveries, their report and the CSV to that run through a `run` parameter, while audience, content and activity stay about the whole campaign.

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

## Verification suites are kept for regression

Unit and integration tests check rules one at a time. Whether the system as a whole keeps its promises under faults is checked by three suites in [tests/verify](tests/verify/README.md), kept in the repository so a change to sending, retries, the limiter or Redis can be measured against earlier runs: a load suite of full campaigns under provider faults, outages, crashes, lost data and clock steps; security probes from outside; and a mutation suite that breaks the code on purpose and expects the tests to notice. Each run saves its results, and what the runs found is logged in `FINDINGS.md`.

They run on their own compose project, image and ports, next to the demo, and the stack is pinned to the mock model, so a load run creates a campaign for every scenario without paying for any. The mock provider enforces 100 calls a second per channel there, as a real provider would, so the provider's 429 is tested as the last check, while the limit itself is judged by the requests that arrive. A suite only counts if it tests something: each mutation case first runs its tests on the clean source, each load scenario must face its event, and a stack-free test feeds the load checks a bad result for each rule. A load run also counts its messages against the audience the review counted on each channel, read before dispatch, so a message lost before it was queued shows up, which no rule about what was queued can catch. The clock scenarios step the wall clock of Docker's VM, which every container on it shares, so they are run apart from a demo in use.

## Signal, our own design language

The product follows the pattern shared by Klaviyo, Insider One and Braze: describe the goal, review a plain-language draft, then approve. It uses a guided workspace rather than a chat, because this flow has one fixed path. Mock provider acceptance is labelled `Accepted`, not delivered, because nothing confirms delivery to a person. The first version was light only, to keep scope on the core flow; Settings now offers light, dark and the device's choice. See [DESIGN.md](DESIGN.md).

## LLM providers behind one application-level interface

Claude is the primary provider, as the case requires. OpenAI and a deterministic mock share one interface defined by what the application needs, not by a vendor's wire format. Validation stays in the backend, so it does not depend on the provider. A chosen provider without its key fails at startup; there is no silent fallback to the mock.

The OpenAI adapter uses the Responses API with a strict JSON schema and `store: false`, because nothing continues an earlier response and a brief has no reason to stay on OpenAI's side. Strict mode requires every property, while our segment filters are optional, so the adapter rewrites its copy of the schema: an optional property becomes required but may be null. The shared schema stays as it is, and `segment.ParseProposal` reads null the same as a missing filter. This adapter has no fallback model like the Claude one, so a refusal ends the attempt.

## Claude output is shaped by a schema but judged by the backend

Structured output asks Claude for the right shape, but it cannot enforce day ranges or the city list, and the model is still untrusted. `segment.ParseProposal` is the authority: unknown fields, unknown cities and out-of-range windows are rejected, and only a validated `Spec` can reach the compiler, which sends every value as a parameter. An empty spec compiles to `FALSE`, so a bug can never mean "everyone".

Invalid output gets up to two repair attempts, with the validation errors passed back to the model. Refusals, truncation and outages are not retried this way, because the same prompt would not fix them. Output that never validates is not stored. Conditions the filters cannot express are returned as `unsupported_conditions` and block approval, rather than being dropped or approximated.

The adapter enables Anthropic's server-side fallback, so the API can retry a refused or overloaded request on another model. `llm_model` records the configured model, so a served fallback is not visible in that column.

The Claude adapter rewrites the schema the same way as the OpenAI one: every property is required, and an optional one may be null. Claude takes the null branch as `anyOf`, because it refuses an enum under a type list that includes null. With optional properties, Claude lost filters it had decided on. On 2026-11-07 Haiku 5.5 never wrote `cart_added_within_days`, dropped `not_messaged_within_days` and put zeros in filters the brief did not name, so 20 valid briefs were refused, mostly cart and combined ones. To tell whether that came from Haiku or from the size of the model, the same cases were run once on Sonnet 5.5. It failed the same way, with 23 refused, so the cause was not the model. The model's own assumptions still named the 30-day cart window, so the choice was right and only the output lost it, which pointed to the schema. With the rewrite, the 30 briefs that had failed on either model went from 6 to 24 as expected on Haiku.

## The audience is counted as of the proposal time

Windows such as "last 30 days" are measured from `audience_as_of`, so the number under review does not shrink as time passes. Event data is still live: a purchase recorded after the proposal can change the count. For a one-time send, approval then stores the recipient list (see below); a repeating send finds who matches at each run.

## Telemetry level: production essentials, not a platform

Prometheus metrics, `slog` JSON logs, OpenTelemetry spans on critical flows, and a business audit table. Logs stay on `slog` because the Go OpenTelemetry log SDK is less mature than its tracing and metrics. Grafana, Loki, Tempo, Alertmanager and a Collector are left out on purpose: they would take time from the core flow without changing what this case demonstrates.

Metric labels use fixed values only. Validation failures carry a reason code, because the human-readable message can quote model output. Campaign IDs, user data, briefs and message text never become labels. HTTP metrics use the route pattern, so IDs and scanner traffic cannot grow the series count.

Telemetry says how the system behaves; `audit_events` says what was decided. The audit table is append-only and written in the same transaction as the change it describes, so it does not depend on telemetry retention.

## A campaign workspace, with creation as a flow inside it

The app opens on an overview rather than a form, because the system manages campaigns over time: `/` is the overview, `/campaigns` the list, `/campaigns/new` and `/campaigns/:id/edit` the creation wizard, and `/campaigns/:id` the detail page. Everything shown comes from the database, and sections without real data do not exist yet: the Deliveries tab, for example, arrives with that stage rather than as an empty placeholder, and the Content tab only appears once a campaign has messages. Pages beyond the campaigns, such as Settings, Templates, Analytics and the delivery monitor, were added only once they had real data or real choices behind them. Activity stays a tab on the campaign rather than a page of its own.

Each status belongs to at most one of the overview's groups (drafts, active, completed, needs attention), so its counts match what those filters show. `COMPLETED_WITH_FAILURES` sits under needs attention, where someone will look for it. The list's row of status tabs adds narrower groups inside them, shown only while they hold something, with counts that follow the search and channel filter: needs approval within drafts, and repeating, sending now and paused within active. These overlap on purpose, since a repeating campaign that is sending belongs to both.

Once campaigns repeat, the time a campaign was created says little about it, so the list is ordered by what sends next, then by what sent last. Created is only a sort option. Each row says in a second line what kind of campaign it is, in the words of its schedule, and shows the next send and the last run's result instead of everything ever sent added up. A repeating campaign between runs reads "Waiting for next run", not "Scheduled", because it has already sent. The next few sends across the workspace sit above the list. Copies are numbered from the original's name ("(copy)", "(copy 2)") so a list of them can be told apart.

Campaigns carry up to ten free-text tags for sorting them by the marketer's own words, such as a season or a team. A tag another campaign already uses keeps that campaign's spelling, so "sale" and "Sale" stay one filter. The list filters by tag and its search matches tags as well as names and briefs.

A campaign leaves the list in one of two ways. Archiving keeps everything: its report, deliveries and audit trail stay readable, it moves to an Archived tab, and it can be restored. Nothing sends from the archive, so archiving a scheduled campaign cancels its later sends, and restoring it does not bring the schedule back. A campaign still writing or sending messages has to finish first, so nothing is left half done out of sight. Deleting is only for a campaign that never sent or was scheduled. One that sent holds the record of who got what, which the rules about messaging people again read, so it can only be archived. The audit trail cannot change, and its rows point at the campaign, so a delete marks the campaign row with `deleted_at` and every page and count leaves it out; nothing about it can be opened again.

The brief sits above the tabs on the detail page with its own accent, because it is the first thing someone opening a campaign wants to know and it should stay in view on every tab. A long brief opens shortened.

A campaign starts with a provisional name cut from its brief. The proposal suggests a better one, which passes the same validation as the rest of the model's output, and anyone can rename the campaign. `name_source` records who chose the name, so a suggestion never replaces a name someone typed. Renaming works in every status, because a name labels the campaign rather than being part of the immutable approved version. It is still recorded in the audit trail.

The Activity tab is written from `audit_events`, not from spans. Each entry names who did it, from the sign-in name, or says the system did it.

## Streamed content is a preview, and only the finished output counts

Messages stream to the browser field by field, because waiting in silence for a model feels broken. That text is display only. A small parser pulls the fields out of the partial JSON, but nothing is stored or marked as validated until the complete output passes `content.ParseGenerated`. If validation fails and a repair runs, or Anthropic's server-side fallback restarts the response on another model, a `reset` event clears the screen, so abandoned text cannot be mistaken for the result. On failure the browser goes back to what the backend last accepted.

Generation is tied to the request: closing the page or pressing Stop cancels the call and puts the campaign back as it was, and that is not recorded as a failure. `POST /generate` claims the campaign with a conditional update, so two generations cannot run at once. A claim older than five minutes, longer than every attempt combined, is treated as left behind by a dead process and can be taken over. Each claim carries a token that every write of the run checks, so if the process was only stalled, it finds its claim gone and changes nothing. Browsers' `EventSource` cannot send a POST, so the client reads the stream with `fetch`.

## Limits are checked against the longest name that will really be sent

`{{first_name}}` has a different length for everyone, so checking the template as written would let a message pass and then exceed the limit for some recipients. Limits use the longest first name among people who consented to that channel, or the fallback if it is longer. The editor counts the same way, and previews offer the longest name and a missing name alongside a typical one.

## A discount mismatch stops the model but only warns a person

The model may not invent or drop an offer: a percentage that differs from the brief fails validation and goes through repair. An instruction such as "make it 40%" becomes the source for that run, because the marketer asked for it. A marketer's own edit can also change the offer, since they are the authority on it, so it passes with a visible warning and stays visible until the brief and message agree. Length, variable and empty-field rules are hard for both, because a person editing cannot make an SMS fit by intent.

## Changing the audience clears the content

Messages are written for a specific audience, and its longest name sets the limits. Keeping them after a change would leave text checked against limits that no longer apply, so a new proposal puts the campaign back to a draft without content, and the page says so before it happens.

## A campaign chooses its channels when it is created

A campaign uses SMS, email or both, and that choice is fixed once it exists. It is stored on the campaign rather than derived from which text was written, because the choice decides what gets written: the model is asked only for the chosen fields, text for any other field is dropped whoever wrote it, and deliveries are only created on the chosen channels. Reachable counts are zeroed for an unused channel before the approval checks run, so a campaign is not blocked by missing consent on a channel it does not use, and the number on the approve button stays exact.

## Emails are filled with text, never written as markup

The model and the marketer write text only: subject, preview text, headline, body, button label and link. The backend puts that text into a table-based responsive layout with `html/template`, so neither can add markup, and the plain text alternative comes from the same fields. Letting a model write HTML would need a sanitizer, would break differently in each mail client, and would make every campaign a new design to check. Layout and images come from the design a person picks, never from the model.

The preview calls the same renderer through an endpoint instead of copying it in the frontend, so what a marketer checks is what is sent. It is shown in a sandboxed frame with no scripts. The template editor's frame also has `allow-scripts`, because Safari silently drops the page's click listeners on a frame without it; its render carries `script-src 'none'`, so nothing inside the email can run. Sent emails do not carry that line.

A button link from the model must appear in the brief, the instruction or the current version, because a made-up address sends real people somewhere nobody chose. A person editing the message can set any full http or https link.

## Unsubscribing takes a signed link and a button

Every email has its own link, signed with HMAC-SHA256, so it needs no session and cannot be changed to withdraw someone else's consent. Opening it only asks, because mail scanners open links; the POST withdraws email consent and can be repeated. SMS consent does not change. The worker reads consent again before each send, so queued emails for that person end as `consent_withdrawn` with no extra code. The worker refuses to start without `UNSUBSCRIBE_SECRET`, so no campaign can go out with a footer that does not work.

## Tone, discount and recent message exclusion are optional form fields

The brief can still say all of this, so the fields stay optional and a marketer who writes everything in the brief loses nothing.

A discount on the form is added to the brief that the writer and the discount check read, so it follows the same rules as one in the text. A brief naming another percentage is refused, because guessing which offer is meant could send the wrong price.

Tone goes to the model separately and changes wording only. It has no validation beyond the allowed values, since whether a text sounds formal is for the person reviewing it.

"Skip people messaged recently" is a filter in the segment rule rather than a separate step, so counts, previews, the approved version and repeat runs all apply it the same way, and it shows in the conditions. It counts `SENT` and `UNKNOWN` deliveries from any campaign before the time the audience was counted, because an `UNKNOWN` message may have arrived and a campaign's own messages must not shrink its audience. A campaign sent right after approval uses the audience counted at proposal time, so anything another campaign sends in between is not excluded; repeat runs count afresh.

## Tried against a real model

The example brief ran through the app with OpenAI's `gpt-6-luna`: the audience, streaming, validation, approval and sending all worked, and the first text arrived after about 4.5 seconds. It chose the same filters as the mock, wrote natural Turkish, followed a formal tone, used the discount from the form and left the button out when the brief had no link. It did not follow a brief telling it to forget its rules. On 2026-11-07 an English brief ran through the app with `claude-haiku-5-5`, after the schema change described above: "Remind customers in İzmir who added something to their cart in the last 14 days but didn't buy it. Give them 15% off." with a link. It chose the city, the 14-day cart window and no purchase since the cart, wrote SMS and email in all five languages of the audience with the 15% and the link, and the approved campaign sent 165 messages, all accepted by the mock provider, with the link on the email button.

That run showed one limit. Every percentage in a brief must appear in every message, so a brief naming 10% and 90% cannot pass even when the model picks sensibly. Generation then stops with nothing stored, and an instruction naming the discount settles it, while the review page keeps pointing out the difference. A brief with two real offers has to be written as separate campaigns.

## The audience rule is shown as a breakdown

The marketer checks the rule in a breakdown table: from the main audience down through each condition, with what was selected, the brief's wording for it and how many people remain. Counts apply the conditions one at a time at the audience's own timestamp. The raw JSON rule stays out of the interface because it adds nothing a marketer can act on. A discount is not a condition, so it sits with the message settings and only exists once someone adds it.

## Approval freezes the audience counts

Counts used to be recalculated whenever a page opened, so a later consent change could move the number on a finished campaign. Approval now stores the counts and the step-by-step breakdown with the version, and the Audience tab says "snapshot" only when one exists. What was really sent stays a separate figure read from the delivery records, because consent is checked again at send time.

## Each condition shows the words it came from

The model returns, for each filter, the exact words of the brief or refinement behind it. The backend keeps a quote only if it really appears in that text, ignoring case and spacing, and a filter without one is shown as added by the AI. A false quote costs only the explanation, so it does not reject the proposal. The check proves the words were written, not that the filter means the same thing, which is what human approval is for.

## Regenerating replaces a channel, and a missing name uses the reader's language

Regenerating a channel rewrites its messages in every language and leaves the other channel as it is, including the marketer's edits there. Each channel is checked against the same brief, so the offer stays the same across them. A person with no first name is greeted with their language's own word ("müşterimiz", "there", "Kundschaft"), and the email footer is written in the reader's language too.

## Scheduling repeats a campaign, and the schedule says who may get it again

A campaign can be sent now, once at a chosen time, at regular intervals, or on chosen weekdays. Marketers pick from these forms instead of writing a cron expression, which is easy to get wrong and hard to read back. A loop in the worker plays the part of the cron job, and its state lives in `campaign_schedules`, so it survives restarts.

Each run counts the audience again. Whom it may reach a second time is a choice on the schedule, shown only for the repeating forms, as Braze's re-eligibility and Insider One's "multiple times" setting do:

- **Only people who haven't received it yet** (the default): nobody an earlier run reached is queued again, so a repeating campaign reaches people who start matching later, and a run can correctly queue nothing.
- **People who haven't received it in the last N days**: someone reached before is queued again once N days have passed since their last message from it. N starts at one interval, or a week for chosen days. This is the abandoned-cart case: the same person can leave a cart again next month.
- **Everyone who matches, every time**: the picker warns that people who keep matching get every send.

In every case someone whose message from an earlier run is still being sent is left out, and people who were only skipped, for example for lack of consent, count as not reached. The rule is part of the schedule, so the audit trail and the campaign page describe it with the timing.

Missed runs are not replayed one by one. After downtime one run covers them, because each run already reaches everyone who is still owed the message.

A campaign that finished sending can be scheduled again, with the messages already approved. That send is another run, so the same choice applies, and it is shown for a one-time send too, because there are earlier sends to leave out. The default still reaches only people who have not received the campaign, which can be nobody. A cancelled schedule stays on the campaign page marked as stopped, so its rule does not read as if it were still running, and a new schedule replaces it.

A one-time send, now or at a set time, goes to the people who were approved: approval stores the list, because someone signed off on that audience. New purchases or moves afterwards change nothing. Consent is the exception, checked again when queuing and before each message, and so is a "not messaged recently" rule, because a message sent since would break what the campaign promised. A repeating send finds who matches at each run, because its point is to reach the people who match then, and the UI says the number can change. Approvals from before the list existed read the rule when they send.

Known limit: a person who starts matching later can have a longer first name than anyone checked at approval. If their SMS then exceeds the limit, it ends as FAILED after a provider 400 rather than being cut.

## The rate limiter is a sliding window in Redis

A fixed one-second bucket allows up to 200 messages across a bucket boundary. A Lua script keeps a sliding window for each channel, so no one-second span goes over 100, however many workers run, as long as Redis's clock does not step forward by more than 100 ms. If Redis is unavailable, messages wait and no attempt is used: sending without the limiter would risk breaking a hard limit.

A call counts from when it goes out until its answer comes back, and for 1.1 s after the answer. The provider received the call before it answered, so a call held up on the way, when a machine stalls for a moment, cannot land in the same second as a later batch. Before this, a call counted for 1.1 s from when it went out, and a batch delayed by 150 ms or more could reach the provider together with the next one: once in eight measured sends, the provider saw 125 calls in a second. Answers ride on the worker's next request to Redis, which it makes anyway, so this adds no round trips and nothing in PostgreSQL. Spreading permits evenly was the other fix tried, on the branch `experiment/single-permit-limiter`; it cost about 9% of the rate and twice the PostgreSQL CPU.

The window lives only in Redis, which this setup runs without persistence. When Redis loses it, by a restart, a flush or an eviction, the calls of the last seconds are forgotten. Measured with 10 flushes during a send, the provider then saw up to 193 calls in a second. Writing answers back from the workers did not help: most calls of that second had already been answered and reported, so no worker still knew them. A key with no expiry now marks each channel's window as whole, and when it is missing the channel waits 8 s, as long as an unanswered call can count, before granting anything. The same 10 flushes then stayed at 100, at the cost of those 8 s per loss and once at first start. Redis persistence would not replace this check: it cannot cover a flush or an eviction, and, as the next paragraph shows, a snapshot loaded after a restart defeats it.

Redis must also start empty after a restart. The `redis:7-alpine` image snapshots to disk by default, about once a minute while sending, and loads the snapshot when it starts again. That brought back an old window together with the key that marks it whole, so the hold never started: killing Redis five times during a send let the provider see up to 116 and 123 calls in a second. `compose.yaml` now runs Redis with no snapshots and keeps `/data` in memory, and the same crashes stayed at 100, at the cost of the 8 s hold per crash. Nothing in Redis needs to outlive a restart: the window, the pauses and the credential lock all describe the last few seconds or minutes, and losing them is what the hold covers.

A call that gets no answer may still reach the provider later, so it counts until the send timeout plus 2 s have passed, and 1.1 s after that. If a worker stops in the middle of a send, its calls count that long too: the channel slows for a few seconds and does not go over.

**Redis's clock steps, in short:** a load run showed a channel going over 100 calls a second, and a clock watcher traced it to a forward step of Redis's wall clock, which times the window. The limiter now counts each call for 1.1 s, a 100 ms margin, and stepping the clock on purpose confirmed it: steps of 70 and 100 ms stayed at 100. Larger steps let a few calls through for one second (104 after a 150 ms step, 117 after a 500 ms step). The provider refuses those with 429s, the worker retries them, and the idempotency key keeps every message accepted exactly once. A hard bound under any clock step would need a single limiter that keeps time with a monotonic clock, at the cost of tying the limit to one process or host. Every step seen in these runs came from Docker Desktop, which steps its VM's clock to match the Mac's, or was made on purpose by the test. On a Linux server whose time service slews small corrections, as chrony does, a step during a send is not expected; it can still come at boot, from someone setting the clock by hand, or when a virtual machine resumes.

Why this trade: the margin costs about 9% of the rate on every send, 96 s instead of 87 s for the verification send without faults, and it removes the overrun seen in practice. Taking the time from the workers' monotonic clock would cover any step, but would tie all workers to one host and add its own small error from the delay between reading the clock and reaching Redis. Steps are rare on a host whose time service slews, as chrony does after boot; Docker Desktop steps its VM's clock, which is where the 70.7 ms step that first went over (119 in a second) came from. The measurements are in [errors-and-retries.md](docs/errors-and-retries.md#when-rediss-clock-steps-forward).

## An earlier timeout makes the result UNKNOWN

A timeout means the provider may have accepted the message. Retrying with the same idempotency key is safe, but if the retries end in failure, that failure does not prove the first call failed. So a delivery with any earlier timeout ends as UNKNOWN unless a later call is accepted.

## A campaign that gets nowhere stops and says so

A 429 or an unreachable provider costs no attempt, so a long outage would otherwise leave a campaign "sending" forever. After 10 minutes with messages waiting on retries and none finishing, it becomes `PAUSED`, and a notification says why. Messages keep their place and idempotency keys, so nothing is lost or failed, and only a person's Resume starts it again, because a provider back after hours may no longer be the moment someone wants this message to go out. Messages that are merely queued do not count, because they may just be behind another campaign's. The 10 minutes count from when the campaign started sending, resumed or began its latest run, kept in `status_since` by a trigger on every change of status. They used to count from `updated_at`, which a rename or a new tag also moves, so renaming a stuck campaign every few minutes kept it from ever pausing.

Notifications read the audit trail rather than a table of their own: what started on its own, what ended and what went wrong are already recorded there, in the same transaction as the change. What someone has seen or cleared stays in their browser, since nobody here has a real account. Clearing removes only sends that ended well, and a problem leaves the list once it is put right, by resuming or by writing the messages again. A campaign that finished with failures stays, because nothing puts it right. The bell checks every 15 seconds.

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

The provider does not lose what it has accepted when it restarts. One journal file on a volume holds each message's idempotency key, inbox entry and unsent report on a single line, written and synced before the message counts as accepted, so accepting is all or nothing. If the write fails the call answers 500 and nothing is accepted. Killing the provider mid-send, with 5% of responses lost, sent 6,000 messages once each; the same run with the key file moved aside delivered 161 of them twice. A crash can only cut off the last line, so that line is dropped and damage anywhere else stops the provider from starting, because starting anyway would forget keys. The journal is rewritten down to what is still needed on start and when it grows, so message bodies beyond the inbox's size do not pile up.

Reports reach the app at least once, as webhooks do. A report is tried with growing waits, up to a minute apart with some jitter, for 24 hours, then dropped and logged. A 4xx is not repeated, since the answer would not change. A crash right after a success sends it again, and the app's `ON CONFLICT DO NOTHING` on `delivery_events` makes that harmless. The provider's counters at `/v1/stats` describe one run of the process and start over with it.

The labels say opened for email and read for SMS and name the test inbox, because real providers report email opens unreliably and SMS has no read signal.

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

The app's connections turn PostgreSQL's JIT off. Its queries are short, and twice a query's estimate passed `jit_above_cost` and compiling it took longer than running it: 114 ms of the report's 334, and most of a list request's 22–28 ms. Optional list filters go into the query only when they are in use, because a reused plan cannot see past `$1 = '' OR ...` and guessed 5 campaigns out of 10,000. See step 5 of [the performance log](docs/performance.md#step-5-the-list-redesign-runs-tags-and-archive).

Work is skipped only where its answer cannot have changed. A finished run's total stands in for counting its deliveries, because a run gets all its deliveries when it opens. Pages watching one campaign share a reading for half a second, and hidden browser tabs do not poll. Keeping the list's sort keys and tab counts on the campaign row, and keeping finished reports, would save more, but each write that changes them would have to update them too, so they wait until the list or the report is slow for real users. See step 6 of [the performance log](docs/performance.md#step-6-work-done-only-when-it-changes-something).

## Messages left in flight are taken back, and may stay unknown

A worker that crashes after claiming a message leaves that row in SENDING with a lease. The worker loop checks for expired leases every second and takes those rows back. We cannot tell whether the provider received the message, so this counts as an attempt with the outcome `timeout`. If attempts run out, the delivery ends as UNKNOWN instead of being sent again or called failed. A stale worker cannot overwrite the result, because `claim_token` no longer matches.

A 429 pauses the whole channel through a shared Redis key, so every worker honours one Retry-After. The pause only ever gets longer and is capped at 30 seconds. If the provider cannot be reached at all, nothing was sent, so no attempt is used and the channel rests for 5 seconds. If the limiter itself is unavailable, nothing is sent until it returns.

`make test-chaos` checks this on a running stack by killing the worker, stopping it, and stopping Redis, the provider and Postgres during a send. In a full run all five ended with every delivery finished and, where the test compares them, the provider's count matching our records. A message taken back after a crash is sent again with the same idempotency key, so the promise of no second message depends on the provider honouring that key, as the mock does.

## A simple sign-in that says who, not who you really are

Signing in takes a username or email and no password. The name goes into an unsigned HttpOnly cookie for 30 days, and every campaign route answers 401 without it. This gives the audit trail a name to record and is not authentication: anyone can type any name or edit the cookie, and the page says so. A real login would replace the cookie handling in `internal/httpapi/session.go` and leave the rest alone.

`audit_events` records `actor_type` and `actor`. A step taken in a request is `user` with the name, work the worker or scheduler does is `system`, and rows older than this change are `unknown`, because guessing a name would falsify the record. `campaigns.created_by` and `campaign_schedules.created_by` keep who created a campaign and who set up its schedule, so a scheduled run can read "by the system, set up by X".

## Schedules live in the database

A schedule is a row in `campaign_schedules`, including its next run time, and the scheduler keeps nothing in memory. Restarting any service or the machine loses nothing, and the data stays while the Postgres volume does: `make down` keeps it, `docker compose down -v` deletes it. Seeding skips a database that already has users. A one-time send whose time passed during downtime goes out when the system returns, and missed intervals become one run.

Closing a campaign takes the same row lock a scheduled run holds while it adds deliveries, then looks for open deliveries again. Without that, a run starting just as the last one drained could be closed over: nothing claims the deliveries of a campaign that stopped sending, so they would sit queued under a campaign marked completed. The look has to be its own statement after the lock, because one statement keeps the view it started with.

## A refinement rewrites the brief

The brief is what a marketer reads first and what message writing starts from, so it has to say the same thing as the rule. A refinement returns the whole brief rewritten next to the new rule, and the two are saved together.

The rewrite is model output, so it is checked like the rest. It is rejected and retried if it is empty, too long, or brings in a discount or link that neither the old brief nor the refinement contains, and a discount that contradicts the one chosen for the campaign is refused with the existing conflict error. A rewrite on a first proposal is ignored, because only a person's request can change a person's brief. The page says the brief changed and names the request behind it, and the audit trail records that it changed but not the text.

## SMS and email are separate steps

One page held seven fields, two previews and a state and origin label on every field, so the setup flow is now Brief, Audience, SMS, Email, Review, without the step of a channel the campaign does not use. Each step shows one state and one origin for its whole message, and an email's optional button stays folded until it has something to say.

The first arrival still writes all channels in one model call, because both messages come from the same brief and are validated together. Regenerating on a step rewrites only that channel, so asking for a shorter SMS cannot throw away edits made to the email. The stored row stays one document per campaign version.

Moving to another step saves what was typed, so there is no separate save button and no way to walk away from unsaved text by accident. Text the backend refuses keeps the person on the step with the problem shown on the field. Every save replaces the whole document, so it names the revision of the messages it was made to. One made to older messages, in another tab or before a regeneration finished, is refused with a 409 instead of quietly undoing the newer text.

## Three built-in email designs came first

Campaign emails looked plain, so the Email step got three designs (Classic, Announcement, Offer), and a marketer picks one and sees it in the preview, which the backend renders with the same code that sends. The choice is stored with the messages, so an approved email is frozen along with its design, and drafts from before this feature keep rendering as Classic.

These three are code, not data, and Classic is still the default. Marketers' own templates came later as documents of blocks rather than markup, with a brand kit for the logo, colors and footer they start from (see [Templates are documents of blocks](#templates-are-documents-of-blocks-drawn-by-our-own-renderer)). A model cannot pick a design: its value is dropped when output is parsed, and regenerating keeps what the person chose.

## The rule and the messages are compared with the brief

Schema validation proves a rule is well formed, not that it is the right rule: a model answering İzmir to a brief about Ankara passed every check, and only a person reading the rule could notice. The backend now reads the brief without a model and compares what it can read: cities, lengths of time, cart and purchase words, and language.

A rule that says something the brief does not is returned to the model with the reason, the same way as a schema failure, and after three attempts nothing is stored. What the rule leaves out is different, because those words may not be about the audience at all, so it is not a rejection. It becomes a blocker the marketer resolves by refining the rule or editing the brief. After a refinement the comparison uses the rewritten brief, so the rule and the brief saved together cannot disagree.

The reader only knows closed lists: listed Turkish endings for a city, listed units and number words for a time, listed stems for behaviour. Reading less than a person does is the point. It cannot prove a rule right, only catch these specific disagreements, so showing the rule with its evidence and asking for approval stays. A list that guesses would block good campaigns, and a false block costs a marketer trust as quickly as a wrong rule does, so every case in the tests has a paired brief that must still pass ("hafta sonu" is not a time window, "%30" is not a number of days, "güncelleme" is not a city ending). A bare name in no list and in no such sentence position still passes this check.

Reading structure also catches words that are not places, such as `Instagram'da gördüğünüz ürünler`, and a person could not refine their way out of that block. So the model may say a name is not about the audience, which lifts the block and is always shown on the page. It cannot do this for a name the brief uses for the people themselves (`İzmir kullanıcıları`, `people in Berlin`). A wrong claim is then visible and a wrong word is no longer a trap.

A list of places can always run out, so a place is also read from how the sentence uses it: `Paris'teki`, `Van'da`, `in Berlin`, `Tokyo customers`. This needs no list of places, and it tells Van the city from "van" the word. The closed lists left are of what is not a place (months, days, languages, words that open a sentence), so an unknown word costs a visible note a person can reword, where an unknown place used to cost a silent miss. The users table and a list of Turkish provinces still catch a bare city name.

Generated messages get the same treatment: a city or number that is not in the brief, the instruction or the audience is refused, so a model cannot add "last 3 days" or a store in another city. This covers generated text only. A person editing a message may write what they like, except a discount that differs between channels, which cannot be approved. Asking one channel for a new discount is refused up front, because the other channel keeps its text and the offer would split.

## The benchmark draws the limits, it is not a target

`tests/benchmark` holds 247 hard briefs, refinements and content instructions with their expected answers. `make test` runs the checks on them with no model, and `make benchmark` runs them through a real model and writes a report to `tests/benchmark/results`. With no model, the backend has 35 known gaps today: 4 of 134 correct answers are blocked, 13 of 525 wrong answers get through, and 18 of 66 answers that drop a condition get through; 12 of those 18 are briefs asking for data the platform does not have. They are listed in `tests/benchmark/known_gaps.txt`, and the test fails on any gap not in the list and on any listed gap that closed, so a regression cannot pass as a measurement and a fix updates the list and these numbers with it.

The first run changed a design rule. Three of its four silently wrong audiences came from our own check: the model read `alışverişi olmayan` correctly, our narrower reading called that a contradiction, and the model obeyed the correction. A wrong correction is worse than none, so the backend now refuses only a clear opposite. Where it is unsure it leaves the answer alone. Blocking rarely hurts, and the checks are not tightened further to chase the remaining cases.

The latest reports, one run of every case on each model, with the same prompt and cases ([gpt-6-luna](tests/benchmark/results/2026-11-07-0447-openai-gpt-6-luna.md), [claude-haiku-5-5](tests/benchmark/results/2026-11-07-0451-anthropic-claude-haiku-5-5.md)):

| Result | gpt-6-luna | claude-haiku-5-5 |
|---|---|---|
| As expected | 242 | 241 |
| Blocked without need | 5 | 5 |
| Shown as unsure, not blocked | 0 | 1 |
| Silently wrong audience | 0 | 0 |
| Message with forbidden text | 0 | 0 |

Before the schema rewrite above, Haiku had 222 as expected, 20 blocked without need and 2 silently wrong ([2026-11-07-0417](tests/benchmark/results/2026-11-07-0417-anthropic-claude-haiku-5-5.md)). The Sonnet 5.5 run ([2026-11-07-0424](tests/benchmark/results/2026-11-07-0424-anthropic-claude-sonnet-5-5.md): 220, 23 and 2) was only that one diagnostic: it showed a larger model failing the same way, so the work went on with Haiku. Sonnet is not measured again and is not a result of this benchmark; nothing in the numbers above or the defaults depends on it. After the rewrite, a full Haiku run had three silently wrong audiences, and the prompt gained two rules for them: not buying is tied to the cart only when the brief ties it there, and choosing people by what they bought is a condition no filter covers. The third was the case itself: it still expected German to have no users after German became a language. It now expects the German filter, and a new case asks for Russian, which the platform does not have.

One clean run says little about the next one: the run before these changes had one silently wrong audience in 738. These numbers describe one model on cases we wrote ourselves, not real traffic, and they do not carry over to another model. The held-out cases have been read since, so they no longer count as unseen.

## Three levels of telling a person

Marketing tools with AI segments mostly show the conditions and leave the rest to the person. We check more than that, and stay out of the way where we are not sure. What the backend finds goes to one of three levels:

- **Corrected without showing it.** A city, window or language the brief clearly contradicts goes back to the model. If it cannot fix the answer, nothing is stored and the person sees an error.
- **Red: blocks approval.** A condition about who receives the message that the rule does not express. Sending to all of Adana when the brief says newsletter subscribers in Adana is the wrong campaign, so the person has to change the brief.
- **Yellow: shown, does not block.** A reading the backend cannot confirm, such as `ödeme yapmayan` as no purchase, and words the model left out of its split. The person decides.

The promise this makes is narrower than "the audience is never wrong": a contradiction does not pass, a missing condition is not hidden, and nothing is sent until a person has seen it and approved it.

Finding the missing condition does not depend on a list of words. The model splits the brief into parts and says which decide the audience and which filter each one became, and the page shows all of it. The backend only trusts this as far as it can check: a part must appear in the brief, a filter it names must be in the rule, and words no part covers are flagged. An audience part with no filter blocks. This still rests on the model: if it calls a real condition part of the message, nothing blocks, though the words are listed on the page as not used for the audience. A stricter reading of joining words or a longer word list would not change that, so we leave it there.

Where it blocks without need, by choice: words the lists know used in another sense (`yaş fark etmeksizin`) and behaviour the filters only roughly match (`mağazamıza uğramayan`). That costs time and not a wrong send.

## Approval is slower on purpose

The product is not built to send a campaign in as few clicks as possible. Campaigns go out rarely, and a wrong one reaches thousands of people and cannot be taken back, so a few more seconds at review cost less than one wrong send. The approval flow keeps some friction on purpose, in a few chosen places:

- **Every version is opened before approval.** Review lists each language, and each A/B version of it, and the approve button stays off until every one has been opened. A person does not approve a message they were never shown.
- **The audience is shown as conditions, not a number.** The breakdown says which filters chose the people, the brief's words behind each one and how many remain after each.
- **Generating is not approving.** Valid output from the model only makes a draft. Nothing is sent until a named person approves it.
- **What was approved is what was shown.** The review page sends back a revision of the messages, the audience and the languages it showed. If any of them changed after it loaded, in another tab or by a colleague, the approval is refused and the page asks for a reload, so the opened tabs count for the version being approved. The revision is required, not only sent by the page: an approval without one, from a script or another client, is refused too.
- **What was approved stays as it was.** The approved rule, messages and counts are frozen, and later runs of a schedule send that same version.

The friction sits where a person can judge something, and nowhere else. What code can check exactly, it checks first: a city, window or language the brief contradicts goes back to the model, and a message from the model with a number, city or link the brief does not contain goes back too and is not saved. A person is then asked about what code cannot decide: whether this is the audience they meant and whether the message says the right thing. There is no countdown or forced wait on the approve button, because waiting adds time without adding attention.

The limit of this: opening a version shows it, it does not prove anyone read it. A person can click through every tab in a few seconds. The checks listed above the approve button (audience, limits, discount, languages, link, unsubscribe, timing) point attention at what matters, but the last look is still the person's responsibility.

## Each person reads their own language

`users.language` decides what a person receives. One version is written for each language the audience contains and the campaign has not left out, largest first, so a Turkish reader gets Turkish SMS and email, and a German reader gets German. Anyone else gets English.

- The stored person's preference wins over a brief. "Write it in English" still reaches Turkish readers in Turkish, and the marketer sees that as a "Not done" note. A brief is one sentence about many people; the preference is what each of them told us.
- Each version is checked on its own: the 160 and 80 limits use the longest name among that language's readers, or the word for a missing name if that is longer ("müşterimiz" is longer than "there"). Discounts and facts are compared with the same brief.
- Versions must promise the same discount, or approval is closed. A person's language must not change what they are offered.
- Generation is all or none. If one language fails, nothing is saved, so an audience is never left with some messages out of date.
- Approval and the campaign's content show one version at a time, with the languages as tabs above it, so the page stays readable with many of them. Nobody approves a language they did not see: approval stays closed until each version has been opened, and the unread ones are named. The SMS shows on a phone screen, as the person will read it.
- What surrounds the message follows the person too: the missing-name word, the email footer and the unsubscribe label.
- A draft or approved campaign from before this keeps its one version. A draft with more than one language in its audience has to be written again before it can be approved.

This costs one extra model call per language, made together once the first version is written. Translation quality is checked only on numbers, cities, discounts and links; the wording is for the person approving to read.

## Accepted, delivered and bounced are different answers

A delivery attempt is our call to the provider. An event is the provider telling us later what became of the message. A delivery's status only ever comes from our calls, so `SENT` still means accepted, and arrival is read from `delivery_events`.

- The mock provider reports each accepted message once, a few seconds later, as delivered or bounced. A bounced message never appears in the test inbox, because it reached nobody. `MOCK_BOUNCE_RATE` sets how often.
- These reports are simulated, and the screens say so. The report shows measured steps (planned, can receive, called, accepted) next to simulated ones (delivered, bounced), and only the second kind depends on the mock. The label is written in the screen rather than read from the event, since the mock is the only sender there is.
- A bounce wins over a delivered report, as the more specific news. An open proves arrival even if the delivered report was lost.
- A message whose calls got no answer stays `UNKNOWN` even if the provider later reports it arrived, because our calls did get no answer. It is counted on its own line rather than added to delivered, so each step of the funnel stays part of the one before.
- A report that never comes leaves the message waiting, and the screen says waiting. The report page stops asking after a minute with no change. There is no separate "no report" state, because a late report can still arrive and silence does not say what happened to the message.
- A bounce is not retried and does not stop later campaigns from trying that person. A real product would suppress hard bounces; the mock has no hard and soft bounces to tell apart.

## A test compares two wordings and names no winner

A campaign can ask for two versions of its messages. The marketer enters who gets version A, 30 for a 30/70 split, and each person lands on one side from a hash of the campaign and the person, so a retry, a later scheduled run or a second channel never moves them. Only the wording differs: both versions must promise the same offer, which approval checks, because a test of two discounts would be a pricing decision nobody made. Both versions are written, checked and stored together, for every language, and a second version that says the same thing as the first is sent back to the model.

The report shows each side's numbers and declares nothing. Delivered and bounced are simulated and random here and opens come only from the test inbox, so a winner would be a coin toss with a badge on it. Sending the winner to the rest and significance testing wait for a real outcome to measure. The cost is plain: each version per language is its own model call, so two languages take four.

## A copy starts over and an export stays small

Duplicating a campaign keeps its brief, options, audience rule and messages, and leaves approval, schedule and deliveries behind. The audience is counted again from today, and the copy passes the same approval checks, so a message that no longer fits the new audience's longest name is caught. No model call is spent on what is already written.

The CSV holds every delivery the filters keep, not the page on screen, and it leaves contact details out because a report read elsewhere does not need them. Who exported is recorded in the campaign's activity. Accepted is written as accepted and simulated arrival is labelled simulated in the column names, so the file does not claim more than the screens do. Customer text that starts like a formula is neutralized before a spreadsheet can run it.

## Channels show what their calls did

SMS and email go through one mock provider at one address, so a separate "connected" light for each channel would be two copies of one fact. The overview says the provider is reachable once for both, says that they go up and down together, and gives each channel what really differs: when it last had a message accepted, and how many calls it made and had refused in the last hour. No WhatsApp row or connection screen stands in for work that does not exist.

## What was asked and not done is shown

A model can pass over part of a request without breaking any rule, and the screen then looks as if the work was done. Asking the model to admit it is not enough, so there are two sources. The model reports in `not_applied`, and the backend compares the result with the request itself: a refinement that changed neither the rule nor the brief says so, and a number or city named in a request but missing from the result is listed. These are notes, not blockers, because only the person knows whether the outcome is acceptable. Writing or editing that channel again clears its note.

## Messages do not say how a person was picked

"Because you shopped in the last 60 days" is true and still a poor thing to tell someone. The prompt forbids it, and the backend refuses generated text stating a length of time the rule selects on, unless the marketer gives that length a second time, when it is also about the offer. This reads times only, so a selection described without one relies on the prompt and on review. People editing a message may write what they like.

## A stop has a budget

A stopped worker finishes what it holds, and the slowest case is a provider call that never answers. Docker kills after 10 seconds by default, and `delivery.MaxDrain` is 20, so such a call could be killed mid-way. Nothing was lost, since the lease recovers it, but a graceful stop was not graceful. The budget is now a named constant, the worker service waits 30 seconds, and a test fails if the two drift apart. A platform that waits less falls back on the lease, which stays the guarantee for real kills.

## Permission comes before a claim

The worker asks the rate limiter how many sends it may make, then claims exactly that many, one loop and one set of slots per channel. It used to claim first and wait for the limiter with the claim in hand. A channel paused by a 429 then filled the shared slots and blocked the other channel, and a long wait could outlast the lease, so another worker could take the same message. Now a channel with no permission holds nothing. A slot also refills as soon as its call ends, because waiting for a whole batch let one call with no answer stop every other message for 5 seconds.

## The limit is counted as the provider sees it

A limit of 100 a second means 100 as the provider counts them. Calls granted exactly a second apart can land within one second, since each reaches the provider a varying few milliseconds later, and measured on arrival this let through up to 117. Counting each call over 1.1 s from its grant cost about 9% of the rate and still let a call held up by 150 ms or more land with the next batch. Counting each call until a second after its answer fixed that, and in the measured sends the provider never saw more than 100 in a second, at about 95 a second per channel ([performance.md](docs/performance.md#sending-with-the-new-limiter)). Later runs found that a forward step of Redis's clock could still let calls in early, so today a call counts until 1.1 s after its answer, and a channel sends about 85 a second.

What shows the workers keep the limit is the count of every request that reaches the provider, refused ones included. The provider's own 429 only keeps extra messages from being accepted; it says nothing about how many the workers sent. The verification suite counts on arrival for that reason, and treats a single refusal for the provider's limit as a failure outside the scenario that sets the workers above it.

## A permit counts from when it is used and belongs to its reservation

A permit is granted before the claim, so a slow claim sends it late, where it can land in the same second as permits granted after it. Giving back unused permits by taking the newest also could not tell whose they were: a worker stalled past the window would erase sends another worker had made, and the limit would be exceeded by that many. So after claiming, a worker settles its own reservation in one Redis call: unused permits go back and used ones are counted from that moment. If a stall outlasted the window and others filled it, only what still fits is confirmed, and the rest of the claim returns to the queue without using an attempt. The whole claim returns the same way if settling fails: a failed settle used to count as permission, which is the likely reason a chaos run once saw 5 messages go out after Redis had stopped. Settling costs one more call to Redis per batch.

## A failing provider costs messages nothing

Five attempts last under 20 seconds, so a provider answering 5xx for a minute would use up the attempts of every message tried in that time, for a problem that is not theirs. Per channel, when at least 20 calls in 10 seconds include 50% failures, the breaker stops calling, and messages stay queued without being claimed. After 5 seconds, doubling up to 30, three probes go out. They cost no attempt, and if all three pass the channel resumes. Usual failures of a few percent stay far below the threshold, and each worker counts its own calls.

## A refused key holds the channel, not the message

A 401 used to count as a rejected message. With an expired key every message would fail within minutes, and the no-duplicate rule would keep the run from reaching those people again. A 401 now uses no attempt and locks the channel in Redis for every worker. One call every 30 seconds tests the key, and the limiter grants it in the same step that moves the next test's time forward, so there is one across all workers without a lock service of its own. The test carries the lock's generation and only opens that lock, so a late answer cannot open one set since. Refusals while locked change neither the generation nor the next test's time, so they cannot keep the test from going. The key is an environment variable of the worker, so there is no event that says it was renewed: the test is how the channel finds out, whether the key changed with a restart or at the provider. A 403 stays a rejection, as some providers use it for a refused recipient.

## A channel in trouble raises an alert, not a pause

A provider failing 70% of calls kept the breaker cycling: three probes all passing is a 3% chance at that rate, so a campaign sent about 3 messages a minute and would have taken days. The rule that pauses a campaign after 10 minutes did not fire, because a few messages still finished. Pausing on a failure rate was considered and left out: it would stop the healthy channel too, and a provider that recovers would wait for someone to resume. Instead each sending campaign gets an alert per channel and kind, read from the calls the worker already records: most of the channel's last 20 calls failing, nothing accepted while retries wait, or credentials refused. The rate is read over calls rather than minutes, because a healthy stretch just before the provider broke would otherwise outweigh the failures that follow. It opens and closes on its own, closes only after 20 calls in a minute or when nothing is left to send, and is written once under a PostgreSQL advisory lock however many workers check at once. Nothing new is kept in Redis, and failed connections are not written as calls, so reports keep counting only calls that reached the provider. The breaker's own state stays in each worker: sharing it would need Redis to keep state that a worker's crash could leave stale.

## Retrying depends on the provider's idempotency

A call with no answer may have gone through, so a retry is only safe if the provider recognises the repeat. The mock does, by idempotency key. Real ones may not: Twilio documents sending an SMS as not idempotent, and Amazon SES warns that a retry can produce a second email. Connecting one of those means choosing per provider: where repeats are not recognised, a timeout or 5xx ends as `UNKNOWN` rather than being retried, trading a possibly missed message for no double one. That is not built, since only the mock exists today.

## A transaction never asks the pool for a second connection

Approving and dispatching once read the campaign through the pool while holding its row lock in a transaction. Concurrent requests for one campaign could then hold every connection between them: one owned the lock and waited for a free connection, the others waited for the lock. Nothing ended the wait. The pool holds at least four connections or one per CPU, so this appeared on a 4-CPU CI runner with five requests and not on an 8-CPU laptop. Reads inside a transaction now go through it, and they also see the state the lock protects. The campaign tests give the service a single connection, so this mistake hangs them on any machine.

## Choosing languages does not change the audience

Who receives a campaign and how we talk to them are separate decisions. After the audience is counted, the marketer sees the languages in it with their counts and can leave any out except English. Their readers stay in the audience, every count stays the same, and they get English, which is written whenever anyone needs it, including when nobody in the audience prefers it. A brief that selects people by language ("only German speakers") is still an audience condition, which is a different thing.

The choice is stored as what is left out, so a language that enters the audience after a refinement gets its own version unless someone decides otherwise. Leaving a language out removes its versions, because nobody would read them. Adding one writes only that language and leaves approved-looking text elsewhere untouched. English limits count the longest name among everyone falling back to it. The worker needs no new rule: the stored versions already say what exists, and a person whose language is not among them gets English.

Later languages depend only on the first, so they are written at once. Sequential writing would have grown by a model call's wait per language.

## Templates are documents of blocks, drawn by our own renderer

A marketer's template is JSON: logo, image, heading, text, button and divider blocks plus a few style choices. It never holds markup, so a template cannot add a script or remove the unsubscribe footer, which is not a block. `html/template` draws it, which means the app, the test inbox and a real send share one renderer. GrapesJS with MJML would have brought a richer editor, but also a Node compiler and a second idea of what a valid email is; Unlayer keeps storage and custom blocks behind paid plans. A small Vue editor is enough for the campaigns this product sends.

Three blocks are slots: the headline, the message and the button. They can move but not be deleted, because they are where the approved words go, and removing one would drop text a person approved. The model still only fills those fields, so it cannot touch layout, images or links in the design. Words typed into other blocks stay the same in every campaign and are not translated; the editor says so.

A campaign keeps its own copy of the design rather than pointing to a version row, the same way it keeps its audience. Editing a template never changes a campaign that took it. Approval records the design's name and a hash of how it looks.

## Images are redrawn on upload and stay while anything shows them

An upload is judged by its content, not its name or declared type: only PNG and JPEG decode, and the size is checked from the header before any pixels are read. The image is encoded again from its pixels, which removes metadata and anything hidden in the file; a photo's orientation tag is applied first so it does not turn sideways. SVG is refused because it can carry scripts.

Pixels, not file size, decide what an upload costs, because a 0.15 MB PNG of one colour can claim 36 million of them. So an upload may have at most 24 million pixels, which is a 6000 by 4000 photo and about 96 MB once read, and the header is checked first. Anything shown wider than 1200 pixels, twice the email column, is narrowed with `golang.org/x/image/draw`, since the standard library cannot scale; the turn of a photo counts when measuring, and images uploaded earlier stay as they are. PNGs use Go's default compression: in one synthetic measurement the best level took 16 times as long (2.7 s against 0.17 s for 1600 by 1000) for a file 15% smaller. Reading the pixels is not the whole cost: decoding, narrowing and encoding one such image allocated about 290 MB in one measurement, and about 390 MB at 16 bits a channel, from files of 0.1 to 0.2 MB. So uploads take turns: two are read and drawn at a time, the rest wait up to 10 s for a turn, and one that gets none is answered 503 with Retry-After, rather than every request in a burst holding its own few hundred MB. A turn ends once the image is drawn, before it is stored.

Files are named by their hash and never change, and equal uploads share one. An image cannot be deleted while a template, the brand kit or a campaign's content mentions it, so a sent email's picture does not disappear. The file endpoint needs no session, since people reading an email have none. Emails carry a URL built from `PUBLIC_URL`, so outside a local demo that must be a public HTTPS address. Files sit on a Docker volume behind a small `Store` interface that object storage can replace.

## Build and page weight: the cheap gains only

Measured on one machine: after changing Go code, the backend image took 46 seconds to rebuild and now takes about 8, because the module and compile caches stay in BuildKit cache mounts between builds. They never enter the image, the binaries are the same size, and a first build with empty caches still compiles everything, which took 52 seconds. CI starts from an empty machine each time, so it gains nothing from this.

Nginx now sends the app's JavaScript in 106 KB rather than 338 KB and its CSS in 17 KB rather than 91 KB, and files with a hash in their name are cached for a year. `index.html` is revalidated, so a new build is found. Server-sent events are left out of compression, so they still arrive as they are produced.

We left the rest out for now. The API binary is 78 MB and the worker 51 MB, and the worker carries both LLM SDKs through `internal/campaign` although it calls no model, but nothing suffers from that yet. Separate images would lose the shared layer, loading each page separately needs loading and error states for little gain, and the browser already downloads only the font files a page needs. None of this changes model latency or the sending rate.

## Starter templates load once and never overwrite

Setup adds eight templates, their images and an example brand kit, so a new installation can show the Template Studio without anyone uploading first. The images are embedded in the setup binary and go through the same processing and store as an upload.

A row in `starter_content` records that this happened. Without it, every restart would either bring back a template someone deleted or need to tell "deleted" from "never added". A template with the same name or an existing brand kit is kept as it is, which also makes the load safe on an installation that already has its own.

The starter images have no words drawn into them and the templates have no fixed text, so every word in an email follows the campaign's language.

## A saved audience is a rule, and its counts carry a date

A marketer can keep a campaign's audience under a name and start later campaigns from it. What is kept is the rule, copied from the campaign where it already passed validation, never from the request, so a saved audience cannot hold a filter the backend would refuse. Using one needs no model call: its brief says what the message is about, not whom it is for, so nothing is read from it until someone asks to change the audience.

The numbers on the Audiences page are from the last time it was counted, and the page says when. "Count again" sizes it as of now and says what changed, or that nothing did, so the button never looks as if it did nothing. A campaign started from a saved audience is counted as of its own proposal and keeps its own copy, so renaming, recounting or deleting the saved one never touches it. Names are unique regardless of case, the way tags are, and a saved audience outlives the campaign it came from.

## A weekly schedule follows one time zone's clock

A rule such as "Mondays at 10:00" is read in one time zone, which starts as the browser's and is stored with the schedule, so it keeps meaning 10:00 there after a clock change. Everyone in the audience gets it at that moment, not at 10:00 where they are: sending by each person's own time would need a time zone per customer, which the data does not have, and would turn one run into many. Interval schedules are a fixed span between runs, so they drift by an hour against the local clock when it changes. On the night the clock goes forward or back, a time inside the skipped or repeated hour still runs once, at one of the two readings Go's `time` package gives for it.

## Settings shows the setup and does not change it

The provider, the model, the sending limits and the demo data are set by environment variables, and the Settings page shows them with the variable to change and a restart. Changing them from the page would need a store for secrets and a way for running workers to pick up a new provider in the middle of a send. Only choices that belong to one person's browser, such as the theme, can be changed there.

## The delivery monitor refreshes on a timer

The monitor lists, across every campaign, the deliveries still on their way and those that ended badly, with filters by channel, outcome, last problem and time window. Accepted and skipped deliveries need nobody, so they are left out. It refreshes every 10 seconds by default, or as chosen, rather than over SSE as a campaign's page does: it reads across all campaigns with filters, and the SSE stream follows one campaign's progress. A tick waits for the last answer, and an answer later than 15 s is treated as lost, so one slow request cannot stop the refreshing. The filters live in the address, so a refresh keeps them and a link shares them.

## Analytics reports sending, not engagement

The Analytics page compares campaigns over 7, 30 or 90 days: what was planned, accepted, failed or unknown and bounced, provider calls by day in UTC, and campaigns ranked by failure rate, because the question it answers is where sending went wrong. It shows no open or click rates. Opens here come only from the test inbox, and bounces from the mock provider's simulated reports, which the page labels as such; presenting them as marketing results would read as real numbers.

## State lives in many places, so the machines are written down

Only campaigns and deliveries have a status column. The rest of the state that decides what may happen next is spread over other fields: whether a schedule row exists and its `next_run_at`, whether a run has `finished_at`, `GENERATING` doubling as a lock on the model run, `archived_at` and `deleted_at` lying across every status, the channel gate's keys in Redis, alerts read from the latest event, and some state that only the browser keeps. On 2026-11-07 every one of these implicit machines was traced through the code: its states and transitions, the fields that hold it, the invariants it needs, the states it still allows by mistake, and how it behaves after a crash, under concurrent requests and with several workers.

The review found four bugs. Each was reproduced against the test database first, then fixed and given a test. Archive read the schedule's run count, which misses a "send now", so it could put a sent campaign back to `APPROVED`. Two saves of the messages made from the same page state undid each other without a word, so saves now carry `content_revision` and an older one is refused with 409. Leaving out the only language with messages wrote `NULL` and failed. And a rename or a new tag moved the clock that pauses a stuck campaign and raises its alerts, which now reads `status_since`.

A second pass looked for invalid states reachable through concurrent requests and retries, and found three more. Each was reproduced and given an issue, a pull request of its own and a test that fails without the fix. An approval froze whatever was stored when it arrived, not what the review page showed, so it now names the revision it was given on (#2). A generation read the campaign before claiming it, and could save messages for a replaced audience or undo an edit, so it now reads and claims under one lock (#3). An archived campaign could still write messages and change its audience (#4). A review of those fixes then made the review revision required, so a direct API call cannot approve unseen either (#9), and gave the writing claim an owner, so a run that lost its claim to a takeover can no longer save, fail or keep the newer claim alive (#10).

The risks that were found but not proven are recorded rather than fixed. Most status rules are kept by the code under the row lock rather than by constraints. A lease that expires counts an attempt even when the call never left. `CANCELLED` is allowed but nothing sets it. The approval reading gate exists only in the browser. Each machine, these limits and a checklist for adding a transition are in [docs/state-machines.md](docs/state-machines.md), so a new transition is checked against the same rules: the row lock first, one transaction, and `hasSent` for anything that depends on whether the campaign has sent.
