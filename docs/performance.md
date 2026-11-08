# Performance profile

Measured on 2026-11-05. The goal was to explain where each lost millisecond goes, not to collect more metrics. After the measurements, the code behind each problem was read to find its root cause.

This profile is the **baseline** of an optimization story, tagged `perf-baseline` in git. Every change after it gets a step in the [optimization log](#optimization-log), measured the same way and compared with these numbers. Everything up to the log describes the baseline.

## Summary

The bottleneck is the API's read queries: PostgreSQL runs out of CPU, while the Go code, the queue and Redis are not the limit. Sending at 50–52 messages/s is not lost capacity. It is the correct response to the mock provider's random 429s.

| Question | Answer |
|---|---|
| Where do the 50–52 messages/s go? | Without faults a channel sends 89.5 messages/s against a ceiling of 90.9 (1.1 s window). The rest of the drop comes from the 1 s pause after a 429, which takes 67–68% of the channel's time. Claim, Redis and recording together take about 1%. |
| How long does a channel wait after 429s? | 170 s per run: 143 s of 429 pauses and 27 s of limiter window. When the 429 rate goes from 0.5% to 4%, the rate falls from 76 to 43 messages/s. |
| Why does the API saturate? | Every request recounts history. PostgreSQL uses 7 cores while the API uses 7%. The earlier 147 RPS depends on data size: about 96 successful RPS with one campaign and about 50 with 26 campaigns. |

Two more findings. Docker's 64 MB `/dev/shm` makes PostgreSQL's parallel queries fail under load: with one campaign, 14% of requests fail above 150 RPS. Calls reaching the provider passed 100/s in 3 of 48 channel runs.

Priorities:

1. Give PostgreSQL enough shared memory.
2. Make the campaign detail and overview queries cheaper.
3. Find out why calls pass 100/s at the provider.
4. Keep the real wait metrics.

## Scope and method

All measurements ran on one Mac in a separate Docker project (8 CPUs, 7.75 GB Docker VM). The k6 load generator ran on the same machine. The LLM was mocked. Unless noted, each scenario ran three times and Prometheus scraped every second.

| Scenario | What changed | Data |
|---|---|---|
| A no faults | 429, 5xx and lost responses off | 10,408 messages |
| B faults | 2% 429 only, 3% 5xx only, and both with 0.2% lost responses | 10,408 messages |
| C latency | Provider latency 0–300 ms, no faults | 10,408 messages |
| D workers | 2 and 3 workers, with default faults and without (3 workers only) | 10,408 messages |
| Sweep | 429 rate 0.5%, 1%, 4% | 7,113 messages |
| E queue | 100k and 1M queued rows, one run each, 180 s window | 84k and 1.02M waiting deliveries |
| F API | 25–400 RPS ramp, pools of 8, 16 and 32 connections | 474k deliveries, 26 campaigns |
| G combined | 100 RPS of reads and 1 report per second during sending | more than 474k deliveries |

Instrumentation added for this profile:

- The channel loop's time budget, `campaign_delivery_loop_seconds_total{channel,state}`.
- The real wait, `campaign_limiter_actual_wait_seconds{channel,reason}`, and pauses by source.
- Limiter permits granted and unused, and reserve and settle durations.
- Claim stages (acquire, execute, read, decode), queue age, grant-to-send time and calls in flight.
- Connection pool counters, `db_pool_*`.
- One trace across the API, the worker, the database and the mock provider.
- `pprof` on a separate listener, only when `PPROF_ADDR` is set.

Per-campaign rate is measured between the first and the last accepted message, per channel.

## Sending: where messages/s go

The sending path has spare capacity. Without faults a channel sends 89.5 messages/s and the 1.1 s limiter window caps it at 90.9. The main cause of the drop to 52 messages/s is the 1 s pause after each 429 that the mock returns at random for 2% of calls.

| Scenario | Accepted messages/s (min – mean – max) | Attempts/s | Paused | Window full | Claim gap | Idle |
|---|---|---|---|---|---|---|
| A no faults | 89.5 – 89.5 – 89.5 | 89.5 | 0% | 75.3% | 13.0% | 9.9% |
| 3% 5xx only | 77.7 – 82.4 – 83.7 | 85.0 | 0% | 70.0% | 14.4% | 13.2% |
| 2% 429 only | 52.0 – 54.5 – 56.4 | 55.7 | 68.1% | 11.9% | 8.1% | 9.6% |
| Default (429, 5xx, lost responses) | 49.2 – 51.8 – 54.3 | 54.6 | 67.1% | 12.4% | 8.4% | 9.7% |
| C 0–300 ms latency | 88.8 – 89.0 – 89.1 | 89.0 | 0% | 51.1% | 31.8% | 10.7% |
| Default, 2 workers | 57.6 – 62.4 – 65.9 | 65.9 | 61.5% | 19.8% | 7.1% | 10.1% |
| Default, 3 workers | 68.9 – 72.8 – 74.8 | 76.9 | 46.9% | 34.1% | 6.7% | 11.1% |
| No faults, 3 workers | 89.3 – 89.5 – 89.5 | 89.5 | 0% | 84.1% | 4.8% | 9.2% |

Percentages are shares of the time the channel loop ran. Claim, Redis and settle stay at about 1% in every scenario. "Idle" is mostly the end of a campaign, when the smaller channel has finished and the larger one is still sending.

- **From 100 to 91:** the 1.1 s window is a deliberate safety margin. It costs 9 messages/s all the time.
- **From 91 to 52:** each 429 stops the channel for `Retry-After`. The median accepted per second is 45–46, which fits one 429 in about every 50 attempts.
- **Worker count:** without faults it changes nothing (89.5). With 429s, 1 worker gives 52, 2 give 62 and 3 give 73. A possible reason is that more calls are already on their way before a worker learns about a pause. This is not proven.
- **Resources:** the worker uses about 0.06 cores and 25 MiB. In its CPU profile, system calls and the scheduler take the largest share (38%).

The rate falls steadily as the 429 rate rises, which confirms from another angle that the loss comes from the pause.

### One message's trace

The trace now joins the API, the provider and the database. Across 300 delivery traces almost all of the time is the provider call:

| Step | p50 (ms) | p95 (ms) |
|---|---|---|
| `delivery.send` total | 39.76 | 61.05 |
| `delivery.provider` | 38.95 | 60.25 |
| Mock latency (`provider.delay`) | 37.48 | 58.78 |
| Accept at the provider (`provider.accept`) | 0.95 | 3.70 |
| `delivery.record` | 0.67 | 1.35 |

Tracing did not change the rate: 47–50 messages/s with it off and 46–53 with it on, over three runs each. Worker CPU went from 0.05 to 0.07 cores.

## Queue and limiter

The PostgreSQL queue and the Redis limiter do not slow sending down. With 1 million queued rows, claim p50 stayed at 2.6 ms and the rate did not change. These numbers give no reason to replace the queue.

| Queue | Waiting | Accepted messages/s | Claim p50 (ms) | Claim p95 (ms) | Record p95 (ms) | Enqueue (s) |
|---|---|---|---|---|---|---|
| 10k (default) | ~10k | 49–54 | 2.0 | 7.8 | 2.0 | not measured |
| 100k | 84k | ~54 | 2.4 | 8.9 | 2.2 | 5.0 |
| 1M | 1.02M | ~54 | 2.6 | 9.6 | 2.2 | 60.1 |

- **Redis:** reserve and settle p95 are about 1 ms or less in most scenarios, at most about 2 ms. Redis CPU stays below 2%.
- **Worker connection pool:** waiting for a free connection is almost nil: 3 ms in total over 11k acquires with 10k rows.
- **Grant to send:** from a limiter permit to the HTTP call takes 9–23 ms at p95.
- **Enqueueing:** dispatching to 1 million people took 60 s. This is the one costly queue step, and the request holds a connection and the user for that time.
- **Duplicate sends:** no run recorded two acceptances for the same delivery.

### Rate reaching the provider

The limit is passed rarely. In 3 of 48 channel runs in A–D the provider's sliding 1 s window went above 100: 103, 105 and 133. The sweep and combined runs each showed one more, 104 and 106. The 0–300 ms latency scenario never passed it; its peak was 100.

The brief allows at most 100 messages/s per channel, so this is a finding. Two of the five overruns came from the same run, and all came from scenarios with 429s or 5xx turned on. The code section below narrows the cause to two candidates.

## API: why it saturates

PostgreSQL's CPU limits the API. At saturation PostgreSQL uses about 7 of 8 cores (700–750%) while the API process uses 7–15%. Pool size, shared memory, parallel query and cache settings did not raise the successful request rate. It can only rise if the queries get cheaper.

### Capacity depends on data size

The same mix of six endpoints ran on two data sets, three times each:

| Target RPS | One campaign, 139k deliveries: successful RPS | Median (ms) | Errors | 26 campaigns, 474k deliveries: successful RPS | Median (ms) | Errors |
|---|---|---|---|---|---|---|
| 25 | 25.0 | 19 | 0% | 25.0 | 42 | 0% |
| 50 | 50.0 | 20 | 0% | 49.4 | 75 | 0.5% |
| 100 | 95.9 | 29 | 3.8% | 51.6 | 6,610 | 2.0% |
| 150 | 97.0 | 2,040 | 13.5% | 50.3 | 10,084 | 2.3% |
| 200 | 94.3 | 4,680 | 13.7% | 50.3 | 11,172 | 2.3% |
| 400 | 95.6 | 7,132 | 13.7% | 50.1 | 12,438 | 2.2% |

The earlier 147 RPS was measured with 56k users; this data has 116k. The same mechanism lowers the threshold as data grows: about 96 successful RPS with one campaign and about 50 with 26.

### Where the time goes

Database time at a 200 RPS target on the 26-campaign data (`pg_stat_statements`, 45 s):

| Query | Endpoint | Mean (ms) | Share of total time |
|---|---|---|---|
| Channel use: attempts in the last hour | `/api/overview` | 365 | 38% |
| Audience languages and longest name (two queries) | `/api/campaigns/{id}` | 120–137 | 27% |
| Campaign list and its counts | `/api/campaigns`, `/api/overview` | 68 | 14% |
| Delivery count | `/api/campaigns/{id}/deliveries` | 97 | 10% |
| Progress count | `/api/campaigns/{id}/progress` | 66 | 8% |

- **Wait type:** nearly every sampled active connection was on CPU. Lock and disk waits were negligible.
- **Go side:** 24% of the API CPU profile is system calls. In the block profile, 95% of waiting is getting a connection from the pool (`pgxpool.Acquire`). The Go code is not the bottleneck.

### Shared memory errors

The API's failed requests come from a PostgreSQL shared memory error. Docker gives a container 64 MB of `/dev/shm` by default. When a parallel hash join fills it, the query fails with `could not resize shared memory segment ... No space left on device`. In the one-campaign run this error appeared 10,414 times and about 14% of requests failed above 150 RPS.

### Settings tried

Target 200 RPS, 26-campaign data, three runs each:

| Setting | Successful RPS | Median (ms) | Errors | PostgreSQL error log lines |
|---|---|---|---|---|
| Default (64 MB shm, 8 connections) | 44.9 | 14,227 | 3.5% | 491 |
| 1 GB shm, 8 connections | 39.8 | 16,007 | 0% | 0 |
| 1 GB shm, 32 connections | 38.0 | 17,000 | 0% | 0 |
| Parallel query off, 8 connections | 43.2 | 15,121 | 0% | 0 |
| Parallel query off, 32 connections | 44.9 | 14,581 | 0% | 0 |
| 1 GB shm and 1 GB `shared_buffers` | 46.5 | 13,591 | 0% | 0 |

A first measurement showed 100 RPS with 32 connections, but 27% of those requests failed fast and only 74 RPS succeeded. With the errors gone, pool size does not change the successful rate.

### Combined load

During sending, 100 RPS of reads and one report per second were requested. The API served 44–47 RPS and 1.6–2.3% of requests failed. Sending was barely affected: 45–52 accepted messages/s alone and 44–50 under load, across six channel runs. Claim p95 rose from 8 ms to 25–37 ms and record p95 from 2–3 ms to 15–19 ms.

## Root causes in the code

After the measurements, the code behind the four problems was read. The common theme is that the API recounts history on every request.

### Shared memory

The `postgres` service in [`compose.yaml`](../compose.yaml) sets no `shm_size`, so Docker's 64 MB default applies. When audience and count queries grow, PostgreSQL picks parallel hash joins and that space runs out.

### API queries

| Endpoint | Code | Why it is expensive |
|---|---|---|
| `/api/campaigns/{id}` | [`service.go:833-870`](../internal/campaign/service.go) | An approved campaign takes its counts from the snapshot, but `segment.LongestNames` and `segment.Languages` scan the live audience on every request. `LongestNames` computes, with a second scan, the longest name that `Languages` already returns. |
| `/api/campaigns/{id}` | [`compile.go:65`](../internal/segment/compile.go) | The "not messaged within N days" condition runs `NOT EXISTS` against `campaign_deliveries`, so its cost grows with the number of deliveries. |
| `/api/overview` | [`queries.go:347-371`](../internal/campaign/queries.go) | The last hour of attempts is counted twice per channel: once for the total and once grouped by second for the busiest second. Under load that is about 320k rows per channel per hour. |
| `/api/campaigns` | [`queries.go:131-139`](../internal/campaign/queries.go) | Each campaign on the page has its deliveries counted by channel and status from scratch. |
| `/api/campaigns/{id}/deliveries` | [`dispatch.go:549-566`](../internal/campaign/dispatch.go) | Every filter lives in one query as `($n = '' OR …)`. With no filter set, the `users`, `delivery_events` and arrival joins still run for every row. |
| `/api/campaigns/{id}/progress` | [`dispatch.go:369`](../internal/campaign/dispatch.go) | `sum(attempts)` is not in the index, so an index-only scan is impossible and every row of the campaign is read from the table. |

A side finding: an approved campaign's language split comes from live data, not from the snapshot, so after approval it can disagree with the snapshot's counts.

### Calls above 100/s

There are two candidates. The code allows both, and the measurements do not show which one it is.

- **The limiter:** permits are stamped when they are reserved and settled, not when the call is sent ([`limiter.go:97-111`](../internal/delivery/limiter.go), 1.1 s window). When a pause after a 429 or 5xx ends, the calls that piled up can leave together. [`DECISIONS.md`](../DECISIONS.md) records this as a known limit.
- **The mock's measurement:** the mock records an arrival after decoding the body, under the `s.mu` lock ([`mockprovider.go:207`, `:322`](../internal/mockprovider/mockprovider.go)). `accept` holds the same lock while it writes to disk and calls `fsync` (`:280`). In the mutex profile, `accept` causes 85% of lock contention. Requests that wait on the lock are counted as arriving together when it is released, which can produce a false peak.

To tell them apart, take the arrival time at the start of the handler, before the lock, and rerun the fault scenarios.

### Wait metric

With one worker, `campaign_delivery_rate_limit_wait_seconds` gives the right total: 169 s against a measured 170 s. What it lacks:

- It has no reason label, so 429 pauses and window waits cannot be told apart.
- It is recorded when the wait is decided, not when it happens.
- Each worker adds its own wait, so with 3 workers one channel shows 364 s.

`campaign_limiter_actual_wait_seconds{reason}`, added for this profile, covers these gaps.

## Findings and proposed changes

The expected effects were not measured. They are estimates from the measured shares and durations. None of the changes has been applied.

| # | Finding | Evidence | Proposed change | Expected effect |
|---|---|---|---|---|
| 1 | PostgreSQL fails parallel queries with the default 64 MB `/dev/shm` | 10,414 error log lines with one campaign, 14% failed requests above 150 RPS; 0 with 1 GB | Give PostgreSQL a `shm_size` in `compose.yaml` | 5xx under load goes away; the rate does not rise |
| 2 | Campaign detail recomputes the audience on every request | 27% of database time, 120–170 ms per query | Add the language split to the approval snapshot; take the longest name from the language split instead of a separate query | Most of this endpoint's cost goes away |
| 3 | The overview's channel use counts every attempt of the last hour | 38% of database time, 365 ms under load | Keep per-second attempt counters, or cache the result for a few seconds | With 1 and 2, database time falls by about 65%; capacity on this data roughly 2.5–3× (not verified) |
| 4 | Calls at the provider rarely pass 100/s | 103, 105, 133 in 3 of 48 channel runs; 104 and 106 in separate runs | First stamp arrivals before the mock's lock to rule out a measurement error; if overruns remain, count the limiter by send time | Compliance with the rule; effect not measured |
| 5 | After a 429 the channel stops completely for 1 s | 67–68% of channel time paused; the rate falls steadily with the 429 rate | No change: the behaviour follows `Retry-After`, and the drop comes from the mock's rate-independent 429s | — |
| 6 | The wait metric does not separate causes | 169 s = 143 s of 429 pauses + 27 s of window; 364 s with 3 workers | Keep the real wait and loop budget metrics added for this profile | Loss can be read by cause |
| 7 | Enqueueing 1 million people takes 60 s in one request | Dispatch 60.1 s; 5.0 s for 100k | No change for now; move it to the background if large audiences become a goal | — |
| 8 | The delivery count runs every join even with no filter | 10% of database time, 97 ms per query | Build the query from the filters that are set; add joins only when a filter needs them | An unfiltered count uses the index |
| 9 | The progress count reads every row of the campaign from the table | 8% of database time, 66 ms per query | Add the attempt total to the index or move it to a separate query | An index-only scan becomes possible |

No reason was found to replace the PostgreSQL queue or the Redis limiter. More workers gain nothing when there are no faults.

## Limits

- **One machine:** the load generator, the services and Prometheus ran on the same Mac. This matters for the API results, because PostgreSQL and k6 shared the CPU.
- **Mock provider:** its 429s are random and independent of rate. A real provider usually returns 429 when a limit is passed, so the sending rate may differ there.
- **Single runs:** the 100k and 1M queue tests ran once each with a 180 s window. Other scenarios ran three times.
- **Estimates:** the expected effects of the proposals were not measured by applying them.
- **Unexplained:** the code narrows the 100/s overruns to two candidates, but which one it is has not been proven. Why the rate with 429s rises with more workers has not been proven either.

## Optimization log

Each step is measured with the same scenario as the baseline and compared with it. Steps are listed in the order they landed.

| Step | Change | Finding | Result against the baseline |
|---|---|---|---|
| 0 | Baseline: instrumentation and this profile | — | 26 campaigns, 200 RPS target: 44.9 successful RPS, 3.5% errors |
| 1 | Shared memory for PostgreSQL; approved audience read from the snapshot; channel use counted in one pass and reused for 5 s; delivery filters join only what they read; progress counted from an index | 1, 2, 3, 8, 9 | 200 RPS target: 200 RPS with a 7–10 ms median and no errors, against 77–82 RPS with a 7 s median for the baseline code on the same data |
| 2 | The report reads a campaign's events and calls once instead of once per delivery; index on `delivery_events (campaign_id)` | — (found in step 1) | Report with one client: 109 ms median instead of 287 ms, 9.0 instead of 3.4 requests/s |
| 3 | The mock provider stamps a call when it comes in, under a lock of its own | 4 | No call passed 100/s at the provider in 24 channel runs; the old measurement showed 102, 104 and 117 in the same runs |
| 4 | Each send of a campaign is a run that keeps its counts once finished; the campaign list and progress count only runs still sending | Ceiling after step 3 | Six-endpoint mix on 494k deliveries: 600 RPS with a sub-millisecond median and 800 RPS served, against about 300 RPS after step 3; campaign list query 0.04–0.08 ms instead of 57.7 ms |
| 5 | Checked the list redesign, runs tab, tags and archive against step 4, then fixed what they slowed: one count instead of two, sort keys joined once, group counts over grouped rows, only filters in use in the query, a run's deliveries read by run, JIT off for the app | — (new features) | Mix on 494k deliveries: 800 RPS served as with step 4. With 10k campaigns, the list takes 6.7 ms against 3.3 ms for step 4, after 19.9 ms before the fixes, and the mix holds 600 RPS rather than 800 |
| 6 | Work done only when it changes something: a finished run's kept total instead of counting its deliveries; pages watching a campaign share one reading; hidden browser tabs stop asking | Step 5 | Deliveries tab 1.6 ms instead of 5.2 ms; 20 open pages of a campaign cost 4 queries a second instead of 80; at 800 RPS the top queries take 4.8 s of database time instead of 26–27 s, p95 2.0 ms instead of 6 ms |
| 7 | A call counts against the limit until a second after its answer, instead of for 1.1 s from its grant; answers ride on the worker's next Redis call | Sending at the channel limit after step 6 | Send without faults 88.1–88.8 s instead of 94.0–94.7 s, about 95 messages/s per channel instead of 89; no second above 100 at the provider in 8 runs, against 106 and 125 in one run before |
| 8 | The list's message preview is read from two generated columns instead of out of each version's content | Measured again on 2026-11-08 | Campaign list alone: 2,040–2,261 requests/s instead of 1,764–1,810, against 2,123–2,609 for step 6's code without previews on the same data |

### Step 1: the read path

A short A/B run, not a repeat of the full profile. Both versions read the same data: 116k users and 6 approved campaigns with 232k deliveries, 174k of them accepted within the last hour and 30% reported opened. The deliveries were written directly to the database instead of being sent, so the read path could be measured without a long send. This is about half the baseline's 474k deliveries, so the baseline code does better here than in the profile above. The new code ran first; then the old index and 64 MB of shared memory were put back and the baseline image served the same database. Faults were off, the worker was stopped, and each load ran 40 s, twice.

The same six-endpoint mix as the profile:

| Target RPS | Baseline code: served RPS | Median (ms) | p95 (ms) | Errors | New code: served RPS | Median (ms) | p95 (ms) | Errors |
|---|---|---|---|---|---|---|---|---|
| 100 | 84.0–88.4 | 536–1,397 | 3,366–4,696 | 0.3–0.4% | 100 | 9.8–10.0 | 18.8–19.0 | 0% |
| 200 | 77.1–82.2 | 6,826–7,070 | 13,425–14,390 | 0.3–0.4% | 200 | 7.1–10.1 | 18.7–19.0 | 0% |

With the baseline code, the API's 8 connections were busy all the time and almost every acquire had to wait. With the new code, 1.5–1.7 connections were in use on average at 200 RPS.

Each endpoint alone, one client in a loop for 15 s:

| Endpoint | Baseline: requests/s | Median (ms) | New: requests/s | Median (ms) | Change |
|---|---|---|---|---|---|
| `/api/campaigns/{id}` | 15.6 | 62.5 | 1,543 | 0.6 | about 100× |
| `/api/campaigns/{id}/deliveries` | 33.0 | 29.7 | 185 | 5.2 | 5.6× |
| `/api/overview` | 21.8 | 44.0 | 83.2 | 11.6 | 3.8× |
| `/api/campaigns/{id}/progress` | 49.6 | 19.4 | 97.9 | 10.0 | 2.0× |
| `/api/campaigns` | 58.0 | 16.9 | 56.1 | 17.4 | no change |
| `/api/campaigns/{id}/report` | 3.3 | 291 | 3.1 | 314 | no change |

- The campaign list did not get faster: dropping the channel from its count changed nothing measurable.
- The report is now the most expensive read by far, about 300 ms each. It was not in the profile's mix, and step 1 did not touch it.
- The new code's ceiling was not found, because 200 RPS was the highest target. The 100/s overruns at the provider (finding 4) are not part of this step.

### Step 2: the report

After step 1 the report was the most expensive read. It runs seven queries, and on the largest campaign (81k deliveries) the lifecycle query took 334 ms of them:

- For every delivery it looked up that delivery's events and checked for a provider call, which meant 81k index lookups in each table.
- That plan's cost estimate was high enough for PostgreSQL to compile the query with JIT on every request, which took 114 ms of the 334.

The lifecycle and variant queries now read the campaign's events and calls once and join them by hash. The lifecycle query fell to 79 ms, and its estimate is now too low to trigger JIT. A new index on `delivery_events (campaign_id)` keeps that read to the one campaign; without it, every campaign's events were scanned.

Same data and method as step 1, with the step 1 image against the step 2 image. The report alone was measured for 20 s, and the six-endpoint mix with one report per second for 40 s, twice:

| Load | Step 1 | Step 2 |
|---|---|---|
| Report, 1 client | 3.4 requests/s, median 287 ms | 9.0 requests/s, median 109 ms |
| Report, 20 clients | 12.8 requests/s, median 1,503 ms | 21.3 requests/s, median 919 ms |
| Mix at 200 RPS and 1 report/s | 199.5–199.6 RPS, median 10.0–10.2 ms, p95 19–20 ms | 200.3–200.4 RPS, median 11.8–13.7 ms, p95 24–27 ms |

The mix was already served in full after step 1, so the report change does not show there. Its median and p95 came out a few milliseconds higher in step 2; two runs of 40 s cannot tell whether that is noise. The remaining report time is spread over several queries that each read the whole campaign: lifecycle about 79 ms, provider problems and engagement about 36 ms each.

### Step 3: calls above 100/s

The code left two candidates for the overruns: the limiter, or the mock provider's measurement. The mock recorded a call only after reading its body, and under the lock that `accept` holds while it writes to disk and calls `fsync`. Calls that waited for that lock were counted as arriving together.

The mock now takes the time when the handler starts and records it under a lock of its own, placing each call by that time, because calls can be recorded slightly out of order.

To tell the candidates apart, a temporary build of the mock recorded both peaks in the same runs: the new one, by arrival time, and the old one, taken under the shared lock. 12 runs on the step 1 data, each sending 15,411 messages over two channels: 6 with default faults and 1 worker, 3 with default faults and 3 workers, and 3 with 5xx only.

| Measurement | Channel runs above 100/s | Highest |
|---|---|---|
| By arrival time, under its own lock | 0 of 24 | 100 |
| After the body, under the shared lock | 3 of 24 | 102, 104, 117 |

The overruns came from the measurement, not from the limiter: in the same runs the calls reached the provider at no more than 100 a second. All three were in runs with 5xx answers or 3 workers, as in the baseline. The limit noted in [`DECISIONS.md`](../DECISIONS.md) still holds: if one group of calls is held up on the way by 150 ms or more and the next is not, the provider can see more than 100 in a second. These runs did not produce such a delay.


### Ceiling after step 3

Steps 1 and 2 were measured at 200 RPS at most, on half the baseline's data. This run looked for the new ceiling on data close to the baseline's: 116k users and 13 completed campaigns with 494k deliveries, 371k of them accepted within the last hour. As in step 1, the deliveries were written to the database instead of being sent. The same six-endpoint mix ran at each target for 40 s, twice, with the worker stopped.

| Target RPS | Served RPS | Median (ms) | p95 (ms) | Errors | Busy PostgreSQL connections (of 8) |
|---|---|---|---|---|---|
| 200 | 199.8–199.9 | 10–14 | 45–68 | 0% | 2.7–3.7 |
| 300 | 285.5–299.8 | 14–144 | 69–1,103 | 0% | 5.2–7.1 |
| 400 | 292.0–355.6 | 1,061–2,189 | 3,264–4,482 | 0% | 7.4–7.6 |
| 600 | 280.0–339.3 | 1,974–2,474 | 3,738–4,738 | 0% | 7.6 |
| 800 | 273.6–309.9 | 2,353–2,595 | 4,434–4,835 | 0% | 7.4 |

The ceiling is about 300 successful RPS, against about 45 in the baseline on similar data. Past it, requests wait instead of failing.

At saturation every PostgreSQL connection was running on the CPU, with no lock or disk waits, so PostgreSQL's CPU is the limit again, not the Go code. Database time at 800 RPS (`pg_stat_statements`):

| Query | Endpoint | Mean (ms) | Share of database time |
|---|---|---|---|
| Campaign list with each campaign's delivery counts | `/api/campaigns`, `/api/overview` | 57.7 | 78% |
| Progress counts | `/api/campaigns/{id}/progress` | 15.4 | 10% |
| Unfiltered delivery count | `/api/campaigns/{id}/deliveries` | 9.4 | 6% |
| Response codes for progress | `/api/campaigns/{id}/progress` | 6.5 | 4% |

The next bottleneck is the campaign list. For every campaign on the page it counts all of that campaign's deliveries, about 38k each here, on every request, including campaigns that finished long ago and whose counts can no longer change. Progress does the same for one campaign.

### Step 4: runs keep their counts

A campaign's sends are now runs: the one dispatch of a one-time campaign, or each firing of a repeating schedule. When a run's last delivery finishes, the worker writes its totals, per channel and status counts and response codes into `campaign_runs` in the same statement that marks it finished. Nothing in a finished run changes after that, so the campaign list and progress add up those rows and count deliveries only for runs still sending. The run is also what the no-duplicate rule is about now; see DECISIONS.md.

Same data, mix and method as the ceiling after step 3. The migration turned each of the 13 campaigns into one finished run, in about 30 s.

| Target RPS | Served RPS | Median (ms) | p95 (ms) | Errors | Busy PostgreSQL connections (of 8) |
|---|---|---|---|---|---|
| 200 | 200.0 | 0.9 | 8.1 | 0% | 0.3–0.4 |
| 300 | 300.0 | 0.8 | 7.1–8.0 | 0% | 0.3–0.7 |
| 400 | 399.8–400.0 | 0.7 | 6.6 | 0% | 0.5 |
| 600 | 599.1–600.0 | 0.7–0.9 | 6.3–36.5 | 0% | 0.7–1.8 |
| 800 | 796.1–798.9 | 0.7–4.2 | 6.4–89.8 | 0% | 0.9–4.1 |

At 800 RPS the second run started to queue: connections waited for the pool and the p95 rose to 90 ms. k6 also skipped 42 and 156 of the planned requests there for want of virtual users, and it ran on the same machine. So the ceiling on this machine is between 600 and 800 RPS, against about 300 after step 3.

Database time at 600 and 800 RPS:

| Query | Endpoint | Mean (ms) | Share of database time |
|---|---|---|---|
| Unfiltered delivery count | `/api/campaigns/{id}/deliveries` | 4.0–10.9 | 76–81% |
| Delivery page | `/api/campaigns/{id}/deliveries` | 0.5–2.0 | 10–14% |
| Campaign detail | `/api/campaigns/{id}` | 0.05–0.20 | 4–6% |
| Campaign list | `/api/campaigns`, `/api/overview` | 0.04–0.14 | 2% |

All campaigns here had finished. A campaign that is still sending is counted live for its open run, as every campaign was before; that cost now stays with the runs in flight instead of growing with history. The next bottleneck is the delivery tab's total, which still counts all of a campaign's deliveries when no filter is set.

### Step 5: the list redesign, runs, tags and archive

Three commits after step 4 changed what the read path does. The campaign list now shows each campaign's schedule and last run, counts every status tab, lists the next sends and searches tags. Progress, report and deliveries can be read for one run. Archived and deleted campaigns are left out everywhere. None of this was meant to be faster, so this step checks what it cost and fixes what could be fixed without dropping a feature.

Same data and method as step 4: 116k users, 13 finished campaigns and 494k deliveries, the six-endpoint mix at each target for 40 s, twice, the worker stopped. Step 4's code was measured again on the same day, so both columns come from the same machine state. On this day the step 4 code also served 800 RPS in both runs, where its own measurement had queued in one. Each endpoint alone ran with one client for 15 s.

The first measurement of the new code found two regressions:

| Endpoint, one client | Step 4 | New code | Cause |
|---|---|---|---|
| `/api/campaigns` | 0.49 ms, 1,789/s | 0.90 ms, 1,033/s | Four queries instead of two; the list query itself 0.04 ms to 0.15 ms |
| `/api/campaigns/{id}/deliveries?run=1` | 5.2 ms (no run filter) | 13.8 ms | Filtering by campaign and run made PostgreSQL combine two indexes and read the table |

With 13 campaigns both are small, and the mix still served 800 RPS. The list's cost grows with the number of campaigns, though, so 10,000 draft campaigns were added and the list measured again:

| List, one client, 10k campaigns | Step 4 | New code | After the fixes |
|---|---|---|---|
| Default order | 3.3 ms | 19.9 ms | 6.7 ms |
| Newest first | 3.3 ms | 7.1 ms | 5.6 ms |
| Drafts tab | 4.0 ms | 20.4 ms | 6.8 ms |
| Search | 11.2 ms | 16.6 ms | 14.9 ms |

Step 4 ordered by creation time and counted one total. The new default order puts what sends next first, and it looked up each campaign's next send and latest run in two subqueries per campaign. That means 20,000 lookups to choose a page of 100. The tab counts checked about ten conditions on every campaign.

The fixes, in the order they were tried:

1. The list's total is the chosen tab's count, which the tab counts already hold, so it is no longer counted twice.
2. A run's deliveries are found by the run alone. The run belongs to the campaign, and the run's index answers the count without the table: 13.8 ms back to 5.2 ms.
3. The next send and the latest run start are joined once for the whole list, and only when the order uses them: 19.9 ms to 11.8 ms.
4. The tab counts first group campaigns by status, archive and whether they repeat, then check each tab's condition on those few rows: 3.3 ms to 2.0 ms.
5. Sorting only ids and sort keys and reading the page's campaigns afterwards gained 1 ms. It then lost far more once the next fix gave PostgreSQL true row counts, because it chose to scan every campaign for the page's 100 ids. It was taken out.
6. Filters such as `$1 = '' OR name ILIKE ...` hide from a reused plan how many rows match. The reused plan expected 5 campaigns out of 10,000 and joined runs in a loop for each: 22 ms. The query now holds only the filters in use.
7. With true row counts, the list's estimated cost passed `jit_above_cost`, and PostgreSQL compiled it on every request: 22–28 ms. Step 2 met the same with the report. The app's queries are short, and compiling has not paid off in any of them, so the app's connections turn JIT off unless the database URL sets it: 6.7 ms.

The final code, measured as above:

| Data | Target RPS | Step 4: served, median, p95 | Final: served, median, p95 |
|---|---|---|---|
| 13 campaigns | 600 | 599.9–600.0, 0.7 ms, 5.8 ms | 599.9–600.0, 0.8 ms, 6.3–6.4 ms |
| 13 campaigns | 800 | 799.9–800.0, 0.7 ms, 5.9 ms | 800.0, 0.8 ms, 6.3 ms |
| 10k campaigns | 600 | 600.0, 1.9–3.4 ms, 6.0 ms | 599.9, 5.4 ms, 7.6–7.8 ms |
| 10k campaigns | 800 | 799.9–800.0, 3.4 ms, 6.2–6.3 ms | 799.9 and 734.1, 5.5–6.5 ms, 8.7 ms and 1,361 ms |

With 13 campaigns, the new features cost nothing the mix can see. The list alone takes 0.81 ms instead of 0.49 ms. The deliveries count is still where most database time goes, as after step 4.

With 10k campaigns, the second run at 800 RPS queued: 1,954 requests were never started, and the list query took most of the database time. Each list request still reads every matching campaign twice: once to order them, and once for the tab counts, which take 2.0 ms on their own. Step 4 read them once and counted with an index. Holding the tab counts and sort keys up to date on the campaign row would remove that work, at the cost of updating them on every status change and every run. That was left for when lists of this size are real.

### Step 6: work done only when it changes something

This step looked for work the app repeats although its answer has not changed, and removed only what can be removed without changing what anyone sees. Two larger changes were left out on purpose: keeping the list's sort keys and tab counts on the campaign row, and keeping a finished run's report. Both would be faster, but every write that touches those values would then have to update them, and a missed update would show a wrong number.

The three changes:

1. **A finished run's total is read, not counted.** The deliveries tab without a filter counted every delivery of the campaign on each request, although a finished run keeps its total when it closes and no delivery joins or leaves a run after it opens. Runs still sending are counted as before, and any filter, including one added later, still counts rows.
2. **Pages watching a campaign share one reading.** Each open campaign page asked the database once a second for the campaign and its progress, also while the campaign was only approved or scheduled. A reading is now shared for half a second, so the cost follows the campaigns being watched, not the pages. A page that connects still reads the current state itself.
3. **Hidden browser tabs stop asking.** The health check and notifications every 15 s, and the report every 5 s while delivery reports come in, now wait while the tab is hidden and ask at once when it is shown again if a turn was missed. The delivery monitor already did this.

Same data and method as step 5: 116k users, 13 finished campaigns and 494k deliveries. Step 5's final code was measured again on the same day, then the new code on the same database. Each endpoint alone ran with one client for 15 s. The mix ran at each target for 40 s, twice, the worker stopped.

| Endpoint, one client | Step 5 | Step 6 |
|---|---|---|
| Deliveries, no filter (80,966) | 5.2 ms, 181/s | 1.6 ms, 570/s |
| Deliveries of run 1 | 5.2 ms, 182/s | 1.6 ms, 573/s |
| Deliveries, status SENT (60,842) | 4.7 ms, 203/s | 4.6 ms, 207/s |
| Campaign | 0.61 ms | 0.59 ms |
| Progress | 0.61 ms | 0.60 ms |

The totals were the same in both. A filtered list still counts rows, so it is unchanged.

| Live updates of one approved campaign | Step 5 | Step 6 |
|---|---|---|
| 1 open page | 4.0 queries/s | 4.0 queries/s |
| 20 open pages | 79.7 queries/s | 4.0 queries/s |

| Target RPS | Step 5: served, median, p95 | Step 6: served, median, p95 |
|---|---|---|
| 600 | 600.0, 0.8 ms, 5.8 ms | 600.0, 0.8 ms, 2.2–2.3 ms |
| 800 | 798.2 (73 not started) and 800.0, 0.8 ms, 5.9–6.1 ms | 799.3 (2 not started) and 800.0, 0.7–0.8 ms, 1.9–2.0 ms |
| 1000 | 1000.0, 0.8 ms, 5.9 ms | 998.6 (49 not started) and 999.9, 0.7–0.8 ms, 2.0 ms |

Both versions served the mix up to 1000 RPS; the few requests not started were k6 running short of virtual users on the same machine, in both. What changed is the tail and the database's work. At 800 RPS, the twelve most expensive queries took 26.0–27.2 s of database time in 40 s with step 5, 81% of it the unfiltered delivery count. With step 6 they took 4.8–4.9 s, and the delivery page itself is now the largest share. The p95 fell from about 6 ms to 2 ms because requests no longer wait behind those counts.

Not measured: the hidden-tab change saves requests that k6 does not make, so it does not show here. With 10 tabs left open in the background, it saves 80 requests a minute for the health check and notifications.

### Ceiling after step 6

1000 RPS was the highest target so far, and step 6 served it with most connections idle. To find the ceiling, the same mix ran with step 6's code at 1500, 2000, 3000 and 4000 RPS, 40 s each, twice, on the same data. k6 started with 300 virtual users instead of 50, because at 1000 RPS a few requests had been skipped while k6 added users. k6 runs on the same machine, and Docker has 8 CPUs.

| Target RPS | Served RPS | Median (ms) | p95 (ms) | Not started | Waits for a connection |
|---|---|---|---|---|---|
| 1500 | 1,493.7–1,495.2 | 0.8–0.9 | 26–55 | 193–253 | 10k–17k |
| 2000 | 1,943.9–1,989.0 | 0.9–1.5 | 103–667 | 429–2,238 | 23k–52k |
| 3000 | 2,861.1–2,944.5 | 2.9–3.6 | 154–895 | 1,201–5,543 | 127k–143k |
| 4000 | 3,107.1–3,392.7 | 564–607 | 907–1,076 | 22,986–34,486 | every request |

Up to 1500 RPS the mix is served with a millisecond median and a p95 under 60 ms. From 2000 RPS the tail grows, and at 4000 RPS requests queue: about 3,100–3,400 RPS is the most this setup serves.

The limit is the pool of 8 database connections, not PostgreSQL's CPU. At 4000 RPS PostgreSQL used 2.9 of 8 CPUs on average, about 3 connections were running a query at any moment, and the pool was empty for every request. A connection stays taken while the API sends a query, waits for the answer, reads the rows and sends the next one, so a held connection is busy in PostgreSQL only part of the time. A larger pool would likely raise the ceiling; it was not tried, because the API, PostgreSQL and k6 already share the same 8 CPUs here, and a measurement of that should run on separate machines.

### Sending at the channel limit after step 6

The channel limit is the target that matters for sending: 100 calls a second per channel at the provider. The worker's limiter grants 100 calls in any 1.1 s, so a channel without faults can send at most 90.9 messages a second, and the provider should never see more than 100 in a second. This run measured how long a send takes at that limit, and whether reads at the same time slow it down or push it past the limit.

The same database as above: 116k users and 494k earlier deliveries, one worker, step 6's code. Each send is the Istanbul cart campaign over both channels: 20,514 people and channels, of which 5,103 are skipped by consent or the no-duplicate rule and 15,411 are sent (8,198 email, 7,213 SMS). Four cases, each twice:

- no provider faults, nothing else running;
- no provider faults, while the API serves the six-endpoint mix at 500 RPS and 20 open pages follow the sending campaign;
- the default faults (2% 429, 3% 5xx, 0.2% lost responses), nothing else running;
- the default faults with the same reads.

| Case | Send time (s) | Queue prepared (s) | First message accepted (s) | Email: span (s), msg/s | SMS: span (s), msg/s | Highest calls in a second at the provider |
|---|---|---|---|---|---|---|
| No faults | 94.1–94.2 | 1.19–1.35 | 1.24–1.49 | 91.6–92.1, 89.0–89.5 | 81.3, 88.7 | 100, 100 |
| No faults, reads | 94.0–94.7 | 0.99–1.70 | 1.03–1.79 | 91.9–92.9, 88.2–89.2 | 81.3–81.7, 88.3–88.7 | 100 and 106 email, 100 and 125 SMS |
| Default faults | 154.3–156.8 | 0.86–0.96 | 0.95–1.09 | 152.5–154.9, 52.9–53.8 | 131.0–146.7, 49.2–55.1 | 100, 100 |
| Default faults, reads | 163.2–170.0 | 0.94–1.05 | 1.09–1.27 | 161.5–167.8, 48.9–50.8 | 133.7–142.9, 50.5–53.9 | 100, 100 |

Send time runs from the dispatch call to the campaign's completion. Queue prepared is how long the dispatch call took to choose the audience and queue every delivery. Span is from a channel's first accepted message to its last. Every delivery ended sent in every run, and no message was accepted twice.

| Case | Queued to accepted, p50 / p95 / max (s) | Provider call, p50 / p95 (ms) | API during the send, p50 / p95 (ms) |
|---|---|---|---|
| No faults | 42–48 / 78–89 / 83–94 | 40–44 / 94–165 | — |
| No faults, reads | 42–48 / 78–90 / 82–95 | 41–43 / 98–157 | 3.8–4.2 / 10–221 |
| Default faults | 64–77 / 125–146 / 132–156 | 39–40 / 93–96 | — |
| Default faults, reads | 66–81 / 124–155 / 135–169 | 39–43 / 94–178 | 3.7–3.8 / 10–12 |

Queued to accepted is how long one message waited from being queued to its accepted call. It is mostly the queue: the whole audience is queued at once and drained at the channel's rate, so the last message waits about as long as the send takes.

What this shows:

- Without faults a channel sends at 88–89.5 messages a second, 97–98% of the 90.9 the limiter allows. With the default faults it sends at 49–55, as in the profile: the pause after each 429 takes the rest.
- Reads at 500 RPS do not slow a send without faults. With faults the send took 5–8% longer with reads, within the spread between runs of the same case.
- The API kept a median under 5 ms while sending; its p95 was 10–12 ms in three of the four runs with reads.
- In one of the eight runs, the first with reads and no faults, the provider saw 106 email calls and 125 SMS calls in one second. The worker's own record shows the same second on both channels at once, and the API's p95 in that run was 221 ms instead of about 10 ms: the machine stalled for a moment while k6, the API, PostgreSQL and the worker shared its CPUs, a batch of calls was held up on the way, and it reached the provider together with the next. This is the limit already noted in [`DECISIONS.md`](../DECISIONS.md): the limiter counts calls when it grants them, so calls delayed on the way by 150 ms or more can arrive together. [Sending with the new limiter](#sending-with-the-new-limiter) closes it. The other seven runs, including the three other runs with reads, never passed 100.

### Sending with the new limiter

The overrun above came from counting a call from its grant. Now a call counts from its grant, while it is in flight, and for a second after its answer ([`DECISIONS.md`](../DECISIONS.md#the-limit-is-counted-as-the-provider-sees-it), [errors-and-retries.md](errors-and-retries.md#how-calls-count-against-the-limit)). The provider received a call before it answered, so a later call cannot reach it within that second however long the first was held up, and the window can be a plain second instead of 1.1 s. The same eight runs as above, on the same data, with this limiter:

| Case | Send time (s) | Queue prepared (s) | First message accepted (s) | Email: span (s), msg/s | SMS: span (s), msg/s | Highest calls in a second at the provider |
|---|---|---|---|---|---|---|
| No faults | 88.2–88.8 | 1.12–1.33 | 1.31–1.56 | 86.2–86.4, 94.9–95.1 | 75.9, 95.0 | 100, 100 |
| No faults, reads | 88.1–88.2 | 0.87–0.90 | 0.94–1.00 | 86.7, 94.6 | 75.9–76.2, 94.7–95.0 | 100, 100 |
| Default faults | 153.9–166.2 | 0.95–1.05 | 1.23 | 152.4–163.6, 50.1–53.8 | 139.7–146.2, 49.3–51.6 | 100, 100 |
| Default faults, reads | 158.9–162.8 | 0.93–1.14 | 0.95–1.20 | 157.2–161.3, 50.8–52.2 | 137.6–138.9, 51.9–52.4 | 100, 100 |

| Case | Queued to accepted, p50 / p95 / max (s) | Provider call, p50 / p95 (ms) | API during the send, p50 / p95 (ms) |
|---|---|---|---|
| No faults | 39–44 / 73–84 / 77–88 | 38 / 93–94 | — |
| No faults, reads | 39–44 / 73–84 / 77–88 | 37–40 / 91–96 | 3.9–4.0 / 20–24 |
| Default faults | 70–77 / 129–152 / 141–165 | 37–38 / 91–92 | — |
| Default faults, reads | 65–78 / 123–148 / 139–162 | 39–42 / 94–99 | 3.8–4.0 / 10–28 |

Every delivery ended sent in every run, and no message was accepted twice. With the default faults the provider answered 10–21 calls per channel and run as duplicates: these are retries after a lost answer, and the idempotency key made the provider return the message it had already accepted instead of sending it again.

What changed:

- Without faults a send takes 88.1–88.8 s instead of 94.0–94.7 s, about 6% less, and a channel sends about 95 messages a second instead of 89. What is left below 100 is the second after each answer: a call takes about 40 ms, so its permit comes back a little later than one counted from the grant.
- The provider saw at most 100 calls in a second in all eight runs, including the four with reads and the stalls they cause.
- With the default faults the rate stays at 49–54 messages a second, as before: the pause after each 429 still takes most of the channel's time, and the new count does not change that.
- Recording answers adds nothing measurable: they go with the Redis calls the worker makes anyway, and the worker used 0.05–0.11 CPU cores, as before.


### Measured again on 2026-11-08

The same two measurements as above, sending at the channel limit and the API ceiling, ran again on the code of `main` at ed6f11a. They ran on the verification stack (`tests/verify`) instead of a stack of their own, so the data differs: 116k users, 531k earlier deliveries when the sends began and 695k when the ceiling runs began, one worker, and a mock provider that refuses calls above 100 a second as a real one would. The send is the same Istanbul cart campaign as above, 8,198 emails and 7,213 SMS.

| Case | Send time (s) | Email: span (s), msg/s | SMS: span (s), msg/s | Highest calls in a second at the provider | Retries the provider answered as duplicates |
|---|---|---|---|---|---|
| No faults | 95.8–96.4 | 93.7–94.2, 87.0–87.5 | 82.8–83.0, 86.9–87.1 | 100, 100 | 0 |
| No faults, reads | 95.7–96.1 | 94.0–94.3, 86.9–87.2 | 83.0, 86.9 | 100, 100 | 0 |
| Default faults | 161.2–163.0 | 158.8–161.2, 50.9–51.6 | 136.3–149.3, 48.3–52.9 | 100, 100 | 16–19 email, 17–18 SMS |
| Default faults, reads | 154.9–165.8 | 153.4–164.0, 50.0–53.4 | 136.1–144.8, 49.8–53.0 | 100, 100 | 14–22 email, 18 SMS |

Every run finished with every delivery sent, and no message was accepted twice. The provider call took 37–40 ms at the median and 90–94 ms at p95, the worker used 0.05–0.10 CPU cores, and the API answered the 500 RPS of reads in 3.8–3.9 ms at the median and 10–15 ms at p95.

A send without faults takes about 96 s instead of step 7's 88 s. This is a later, deliberate change, not a regression: a call now counts until 1.1 s after its answer instead of 1 s, so that a forward step of Redis's clock cannot let calls in early ([DECISIONS.md](../DECISIONS.md#the-limit-is-counted-as-the-provider-sees-it)). The extra tenth of a second takes a channel from about 95 to about 87 messages a second. With the default faults the 429 pauses still take most of the time, and the sends stay within step 7's 153.9–166.2 s.

The API ceiling, the six-endpoint mix at fixed rates for 40 s, twice:

| Target RPS | Served RPS | Median (ms) | p95 (ms) | Not started |
|---|---|---|---|---|
| 1500 | 1,500.0–1,500.5 | 1.1–1.2 | 4.2–5.1 | 0 |
| 2000 | 1,994.9–1,999.9 | 1.6–2.8 | 15–87 | 0–204 |
| 3000 | 1,870–2,074 | 966–1,006 | 1,492–1,893 | 35,989–44,079 |
| 4000 | 1,846–2,305 | 910–1,084 | 1,332–1,814 | 66,572–85,057 |

Up to 2000 RPS this is as good as after step 6 or better: the p95 at 2000 RPS is 15–87 ms against 103–667 ms then. Above it, about 1,850–2,300 RPS is served against step 6's 3,100–3,400. As then, the limit is the pool of 8 connections: every request waited for one, while PostgreSQL used 2.9 of the 8 CPUs. `VACUUM ANALYZE` before a run did not change it (2,266 and 2,281 RPS).

To tell the code from the machine and the data, step 6's code (b40add2) was built and run against the same database, taking turns with the current code, at 2000, 3000 and 4000 RPS, twice:

| Target RPS | Step 6's code | Current code |
|---|---|---|
| 2000 | 1,999.2–1,999.9 | 1,971.8–1,997.7 |
| 3000 | 2,540–2,990 | 2,196–2,664 |
| 4000 | 2,560–2,973 | 2,174–2,212 |

Step 6's code reached 2,540–2,990 RPS here, not the 3,100–3,400 it served on 2026-11-06, so part of the drop comes from the setup: more data and other stacks on the same Docker VM. The rest came from the code. Each endpoint was then loaded alone with 16 clients for 12 s. Only the campaign list differed beyond the spread between runs: 1,764–1,810 requests a second against 2,279–2,589 for step 6's code.

### Step 8: the list preview

Since step 6 each list row shows the start of the campaign's SMS or email subject (commit 8b7468b). The list query read it with a lateral join that took each campaign's latest version and pulled two fields out of its `content`. On its own, over the 35 campaigns of this database, that join took 0.86 ms with the two fields and 0.11 ms without. Each list request held a pooled connection that much longer, and with 8 connections that lowered how many requests a second the list served.

The fix keeps the first 160 characters of both fields in two generated columns of `campaign_versions` ([migration 36](../migrations/00036_version_preview.sql)). PostgreSQL fills them on every write, so an edited draft shows its new text at once, and the list reads them instead of `content`. [`TestTheListPreviewFollowsEveryEditOfTheMessages`](../internal/campaign/list_preview_test.go) checks that an edit reaches the list and that the cut falls at 160 characters, not bytes. The rows, the API answer and the page are unchanged ([DECISIONS.md](../DECISIONS.md#indexes-and-query-shapes-follow-measured-plans)).

The same endpoint test, three times each, taking turns:

| Endpoint | Step 6's code | Before the fix | After the fix |
|---|---|---|---|
| Campaign list (requests/s) | 2,123–2,609 | 1,764–1,810 | 2,040–2,261 |

After the fix the list is within the spread of step 6's code, whose own runs differ by about 500 requests a second here. The answer still carries the two previews, about a sixth of its bytes (5.0 of 29.6 kB for 39 campaigns); that cost was not measured apart and is left as it is.

What these runs do not show:

- The API, PostgreSQL and k6 share 8 CPUs, so runs of the same code differ by up to about 20% above 2000 RPS. Differences smaller than that are not findings.
- The ceiling is still the pool of 8 connections, not PostgreSQL's CPU. A larger pool was not tried, for the reason given after step 6.
