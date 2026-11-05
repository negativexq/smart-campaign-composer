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
