# Errors and retries

What the worker does when a call to the provider does not end in a plain 2xx, and how each case counts against the channel's limit of 100 calls a second. For the whole delivery flow, see [flows.md](flows.md#sending-a-message). For the reasoning behind these choices, see [DECISIONS.md](../DECISIONS.md).

## At a glance

| What happened | Next status | Uses an attempt | What the channel does | Counts against the limit |
|---|---|---|---|---|
| 2xx | `SENT` | Yes | Goes on | Until a second after the answer |
| 429 | `RETRYING` after `Retry-After` | No | Every worker pauses it for `Retry-After`, 1 s if absent, 30 s at most | Until a second after the answer |
| 401 | `RETRYING` | No | Every worker holds it; one probe every 30 s | Until a second after the answer |
| Other 4xx | `FAILED` | Yes | Goes on | Until a second after the answer |
| 5xx or 408 | `RETRYING` with backoff | Yes | Counts toward the circuit breaker | Until a second after the answer |
| No answer within 5 s | `RETRYING` with backoff | Yes | Counts toward the circuit breaker | 5 s + 2 s from when it went out, then a second more |
| Provider cannot be reached | `RETRYING` after 5 s | No | Every worker rests it for 5 s; counts toward the breaker | Not at all |
| Message cannot be rendered | `RETRYING` after 5 s | No | Every worker rests it for 5 s | Not at all |
| Consent withdrawn since queuing | `SKIPPED` | No | Goes on | Not at all |
| Failed probe while the breaker is testing | `RETRYING` with backoff | No | Breaker stays open, its wait doubles | Until a second after the answer |

An attempt is one call that may have reached the provider. A delivery has 5.

## Backoff

A delivery that gets a 5xx, a 408 or no answer goes back to the queue as `RETRYING` and waits before it can be claimed again: 1 s after the first attempt, then 2, 4, 8 and so on, up to 64 s. A random extra of up to half the wait is added, so messages that failed together do not all come back together.

When the attempts run out, the delivery ends as:

- `UNKNOWN` if any of its attempts got no answer. The provider may have accepted that call, so `FAILED` would not be true.
- `FAILED` otherwise.

Every retry sends the same `Idempotency-Key`. If an earlier call did reach the provider, the provider answers with the message it already accepted instead of sending it twice.

## 429: the provider asks to slow down

A 429 is about the channel, not the message, so it uses no attempt. The worker pauses the whole channel in Redis for the provider's `Retry-After`, 1 s when the header is missing, and never longer than 30 s. Every worker sees the same pause, and a pause already running is never shortened. The message itself is retried once the pause ends.

The 429 does not count toward the circuit breaker: a busy provider is not a failing one.

## 401: the provider refuses the credentials

A 401 says the channel's credentials were refused: an expired or revoked key, not a bad message. Treated as a rejection, it would fail every message of the campaign within minutes, and the no-duplicate rule would keep them from being sent again in that run. So a 401 uses no attempt and holds the whole channel instead.

- The first 401 locks the channel in Redis, so every worker stops sending on it. Other 401s that arrive while it is locked change nothing.
- Every 30 s, one call across all workers tests the credentials: the limiter hands out that one permit in the same step that moves the next probe's time forward. If the worker making it dies, the next one goes 30 s later.
- If the probe is accepted, its message is recorded as sent like any other and the channel opens. A probe that gets a 401 again uses no attempt. A probe that gets a 429, a 5xx or no answer follows the usual rules and leaves the lock in place, since it says nothing about the credentials.
- A probe opens only the lock it tested. If the channel was opened and locked again while it was out, its answer leaves the newer lock in place.
- The breaker is a separate gate: a worker whose breaker is open does not send even after the lock opens, and a 401 gives a breaker probe back rather than leaving the breaker waiting for its result.
- An alert goes out as soon as the channel locks, without waiting for the checks below. The other channel goes on as usual.

A 403 is still a rejection of the message. Some providers use it for a refused recipient, so treating it as a channel problem would need the provider's own rules.

If Redis loses the lock, as after a restart without its data, the next calls on the channel get a 401, use no attempt and lock it again.

## The circuit breaker

Each channel has a breaker, so that messages wait in the queue instead of spending their attempts on a provider that is down.

- It watches the last 10 s of calls. With at least 20 calls and at least half of them 5xx, 408, timeouts or unreachable, it opens.
- While open, the channel makes no calls. The first wait is 5 s.
- After the wait, 3 probe calls go out. If they succeed, the breaker closes and sending resumes. If they fail, it opens again with the wait doubled, up to 30 s.
- A probe that fails uses no attempt of its message: it says the provider is still down, nothing about the message.

## When nothing moves

A 429, an unreachable provider and an open breaker use no attempts, so a long outage could otherwise leave a campaign sending forever. Every 30 s the worker looks for campaigns that have had messages waiting on retries for 10 minutes with none of them finishing. Such a campaign becomes `PAUSED`, the notification bell says why, and only a person's **Resume** starts it again. Messages keep their place and their idempotency keys, so nothing is lost or sent twice. Messages that are merely queued do not count, because they may just be behind another campaign.

## Channel alerts

The rule above only stops a campaign when nothing at all finishes. A channel can be in trouble while the other one keeps the campaign moving, or while a few messages still get through, and the campaign would then crawl without anyone being told. Every 30 s the worker looks at each channel and raises an alert on each sending campaign with messages waiting on it. Alerts never stop sending: the breaker and the lock above already keep messages from using up their attempts, and a channel that recovers carries on at once.

| Alert | Opens when | Closes when |
|---|---|---|
| Most calls failed | At least half of the channel's last 20 calls, within the last 10 minutes, got a 5xx, a 408 or no answer | At least 20 calls in the last minute with fewer than half failed, or nothing left to send on the channel |
| Nothing accepted | Retries are waiting, the campaign has been sending for 10 minutes, and no call on the channel was accepted in the last 10 minutes | At least 20 calls accepted in the last minute, or nothing left to send on the channel |
| Credentials refused | The channel is locked after a 401 | The lock opens, or nothing left to send on the channel |

- The error rate is read over the channel's last 20 calls rather than a span of time. Over 10 minutes, the successful calls of a healthy stretch, from this campaign or one before it, outweighed the failures after the provider broke, and a measured 70% failure rate raised no alert.
- Each alert opens and closes on its own, per campaign and channel. When the credentials are accepted again while most calls still fail, the credentials alert closes and the other stays open.
- One call that gets through after a bad patch does not close an alert. A campaign with only a few messages left closes it once they are done.
- The alerts are read from the calls the worker records. A provider that cannot be reached leaves no call behind, which the "Nothing accepted" alert covers.
- Workers check at the same moment. Each change is written under a lock on that campaign, channel and alert, after looking at the latest event again, so it is written once.
- An open alert shows on the campaign's page and in the notification bell. Its clearing shows in the bell too.
- 20 calls is a floor, not a promise of coverage: with the breaker open a channel makes a few calls a minute, and a campaign that is paused or has nothing due makes fewer. When too few calls are left to judge a rate, the "Nothing accepted" alert is the one that fires.

A campaign that stops under the rule above, `PAUSED`, stays paused until someone resumes it, even if the channel recovers. With only one channel, a 401 that lasts 10 minutes stops it that way. With two channels where the other one keeps sending, it does not stop, and the alert is what tells someone.

## When a worker stops

A claimed delivery is `SENDING` with a one-minute lease. If its worker stops before recording an outcome, another worker takes it back once the lease expires. That worker may or may not have reached the provider, so the unfinished call counts as an attempt that got no answer. This keeps the outcome honest, `UNKNOWN` if attempts later run out, and stops a message that keeps crashing workers from doing so forever.

On a normal shutdown, a call already in flight is finished and recorded before the worker exits.

## When Redis or PostgreSQL is unavailable

- **Redis down.** No permission to send can be given, so nothing is sent. Messages wait in the queue and use no attempts. Sending without the limiter would risk breaking the provider's limit.
- **Redis loses its data** in a restart without persistence, a flush or an eviction. The window forgets the calls of the last seconds, and a refused channel forgets its lock. A key with no expiry marks each channel's window as whole. When it is missing, the channel sends nothing for 8 s, the send timeout plus 2 s and a second more, so every call from before the loss has stopped counting before new ones go out. A first start waits too, since it looks the same. A refused channel locks again on its next 401. Measured figures are under [Measured behavior](#measured-behavior).
- **Settling permits fails** after deliveries were claimed. The claimed deliveries go back to `RETRYING` after 1 s, with no attempt used.
- **PostgreSQL slow or down.** Recording an outcome waits up to 5 s. A claim whose outcome cannot be recorded stays `SENDING` until its lease expires, and is then taken back as described above.

## How calls count against the limit

The limit is 100 calls a second per channel, as the provider counts them. The worker asks a sliding window in Redis for permits before it claims messages, and every call counts:

1. from when its permit is granted,
2. while it is in flight, until its answer comes back,
3. for one second after the answer.

The provider received the call before it answered, so however long a call was held up on the way, a call granted later cannot reach the provider within a second of it. The answers are sent with the worker's next request to Redis, which it makes anyway, so this costs no extra round trips.

A call that got no answer may still reach the provider later, so it counts for the send timeout plus 2 s, then a second more. If a worker stops in the middle of sending, its calls count that long too: the channel slows for a few seconds and does not go over. A call that never left, because the provider could not be reached, the message could not be rendered or consent was withdrawn, stops counting at once.

## Measured behavior

Each scenario below sent the same campaign, 15,411 messages over email and SMS (8,198 and 7,213), on the profiling setup of [performance.md](performance.md#sending-with-the-new-limiter): one worker, the mock provider with the fault named, 100 calls a second per channel. Without faults the same send takes 88 s. Each scenario ran once.

| Scenario | Send time | Final statuses | Calls per message | Highest calls in a second at the provider | Accepted twice |
|---|---|---|---|---|---|
| 10% of calls get a 429 | 283 s | All sent | 1 for 90%, up to 6; every message used 1 attempt | 75 email, 72 SMS | 0 |
| 30% of calls get a 5xx | 137 s | 15,367 sent, 44 failed after 5 attempts | 1 for 70%, 2–5 for the rest | 100, 100 | 0 |
| 5% of calls get no answer | 135 s | All sent | 1 for 95%, 2–4 for the rest | 100, 100 | 0 |
| The provider stops for 40 s, 20 s into the send | 133 s | All sent | 1; no attempt used during the outage | 100, 100 | 0 |
| The worker is killed 20 s into the send, with 30 calls out | 89 s | All sent | 1, and 2 for the 30 cut off | 100, 100 | 0 |
| 70% of calls get a 5xx for 150 s, then none | 244 s | All sent | 1 for all but 78 calls | 100, 100 | 0 |
| SMS credentials refused for 180 s, two workers | 319 s | All sent | 1; no attempt used on a 401 | 100, 100 | 0 |
| Both channels' credentials refused until the campaign pauses | 749 s | All sent after resume | 1; no attempt used on a 401 | 100, 100 | 0 |
| Redis flushed 10 times, 2 s apart, 20 s into the send | 122 s | All sent | 1 | 100, 100 | 0 |
| The same with two workers | 121 s | All sent | 1 | 100, 100 | 0 |
| SMS credentials refused, Redis killed and flushed at 60 s | 270 s | All sent | 1; no attempt used on a 401 | 100, 100 | 0 |

What each one shows:

- **429.** The channel paused 976 times on email and 789 on SMS, a second each, and spent about 92% of the send paused, so it sent about 29 messages a second. With one call in ten refused, almost every batch includes a 429, so the next pause starts as soon as one ends. No message used an attempt on a 429, and one message was refused 5 times before it went out. A real provider sends 429s when it is overloaded, not at random, and the pause is what it asks for.
- **5xx at 30%.** 44 messages failed after their 5 attempts, close to the 0.3⁵ ≈ 0.24% expected, about 37. The breaker never opened, since fewer than half the calls failed. The other messages went out within 5 attempts: 70% on the first, 21% on the second.
- **No answer.** 791 calls got no answer within 5 s. Each was retried with the same idempotency key, and the provider recognised all 791 as messages it had already accepted, so none was sent twice. The send took 47 s longer: a call with no answer counts against the limit for 8 s in case it still lands.
- **Outage.** Calls the provider could not take used no attempt, and the breaker opened twice per channel. The send took 45 s longer, 40.5 of them the outage itself, and was back at full rate within about 4 s of the provider returning. The provider keeps its idempotency records across a restart, so nothing was sent twice.
- **Worker killed.** The 30 messages the worker had out stayed `SENDING` until their one-minute lease ran out. Another worker then counted each as an attempt with no answer and sent it again. 10 of the 30 had reached the provider before the kill, and it answered those with the message it already had. All 30 went out on their second attempt, and the send took no longer, since the rest of the queue went on meanwhile. The worker's own record shows 114 email calls in one second at that moment, because the 14 email calls taken back are stamped when recovered; the provider saw 100.
- **5xx at 70%.** An earlier build, without channel alerts, ran this with no end to the faults: the breaker opened about 50 times per channel, each channel sent about 6 calls a minute, and after 23 minutes the campaign had sent 128 messages without pausing or saying anything, because a few messages finished every minute. With alerts, both channels raised an error-rate alert 16 s into the send. The breaker held the calls to 78 failures in 150 s, so no message came near its 5 attempts. When the faults stopped, each alert closed 16 s later, once 20 calls in a minute had mostly succeeded, and the campaign finished at full rate.
- **401 with two workers.** The first SMS batch, 32 calls, was refused within the first second, before any refusal could lock the channel. After that the two workers sent one probe every 30 s between them, 5 probes in all, and the credentials alert opened at 1.2 s. Email went on at full rate and finished at 87 s. The fix began at 180 s by restarting the mock provider with good credentials. The probe due at about 181 s did not get through, and the one at 211 s did, so noticing a fixed key takes up to one probe interval, or two when a probe meets the provider mid-restart. The probe opened the lock, the alert closed, and SMS finished at 318 s. No message used an attempt on a 401.
- **Both channels refused.** Both locked at once, each sent one probe every 30 s, and nothing used an attempt. After 10 minutes with nothing finishing, the campaign paused at 616 s, as [When nothing moves](#when-nothing-moves) describes. The credentials were fixed, a person resumed it at 620 s, the next probes went through, both alerts closed at 632 s and the campaign finished.
- **Redis losing its data.** An earlier build forgot the calls of the last second whenever Redis lost its data, and gave that second out again: with 10 flushes the provider saw 189 email and 193 SMS calls in one second. The build measured here holds each channel for 8 s after a loss, and the provider saw at most 100 a second. Each loss costs those 8 s, so the 10 flushes made the send 34 s longer than the 88 s it takes without faults. With refused SMS credentials and Redis killed and flushed, the lock was lost with the data and set again by the next batch's 401s, 32 calls, and probes went on every 30 s after that.

## Where to see it

- **The campaign's Deliveries tab** shows each delivery's status, its attempts and the provider's response codes, and the report lists the problems that ended deliveries.
- **The notification bell** shows paused campaigns and why, open channel alerts and their clearing.
- **Metrics**, without campaign or person labels: `campaign_delivery_attempts_total` by channel and outcome, `campaign_limiter_pause_seconds` by channel and source, `campaign_delivery_circuit_trips_total` by channel, `campaign_delivery_credential_locks_total` by channel and `campaign_delivery_alerts_total` by channel, alert and whether it opened or closed.
