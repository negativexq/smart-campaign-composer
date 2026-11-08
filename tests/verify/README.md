# Verification suites

These suites check the system as a whole, beyond `make test`. Run them before a release or after a change to sending, retries, the rate limiter, Redis, the provider client or anything security-related. Each run saves its results under [results/](results/), so a new run can be compared with the earlier ones. What the runs found is logged in [FINDINGS.md](FINDINGS.md).

| Suite | What it checks | Needs | Time |
|---|---|---|---|
| `load` | Performance, retries and idempotency. It sends a full campaign of about 15,400 messages under provider faults (429s, 5xx, lost answers, slow answers that outlast the worker's wait, an outage, refused credentials), a crashed worker, a worker stopped as a deploy does, PostgreSQL killed for 20 s, lost Redis data, two workers, the wall clock stepped forward, and workers set above the provider's limit. The mock provider enforces 100 calls a second per channel itself, as a real one does, and refuses calls over it with a 429; outside the over-limit scenario any such refusal fails the run. In every scenario: each message ends in a final state, none is accepted twice, the provider counts each sent message once, no channel goes over 100 calls a second, and the expected alerts, pauses and resumes appear. | The verification stack | About 100 minutes for all 24 scenarios |
| `security` | 19 probes from outside: sessions on every route, webhook signatures and replay, unsubscribe link tampering, hostile uploads, SQL and markup in every field, CSV formulas, oversized bodies, nginx paths, logs and error answers. Known open items are recorded, not failed. | The verification stack | About 3 minutes |
| `mutation` | Whether the tests catch broken code. Each case first runs the tests written for that code on the clean source, where they must run and pass. Then it puts one deliberate fault into the source, expects those tests to fail, and restores the file. | `make test-env-up` and the frontend's `node_modules` | About 2 minutes |

## Load scenarios

Every scenario sends the same campaign, 8,198 emails and 7,213 SMS on the default seed, with a fresh mock provider and fresh workers. The mock provider enforces 100 calls a second per channel and counts every request that reaches it, refused ones included. Each run must show:

- **Every message ends in a final state.** The campaign completes, and every message is `SENT`, or `FAILED` where the scenario allows failures.
- **No message is accepted twice.** No delivery has two accepted attempts, and the provider accepted exactly as many messages as the campaign counts as sent, or fewer across a provider restart, never more.
- **No channel goes over 100 calls a second.** No one-second span holds more than 100 requests at the provider. This counts what the workers sent, not what the provider let through: its own 429 only keeps extra messages from being accepted. Across provider restarts, the counters from before each stop are kept and checked too.
- **The provider refuses nothing for its limit.** Outside `workers-at-120`, a single call refused for the limit fails the run.

The two clock scenarios beyond the margin are the exception to the last two checks: they measure how far a channel goes over.
- **The scenario faced its event.** A campaign that finishes before its event, or an event that fails, fails the run.

What each scenario does and what it adds to those checks. Times are from the full run of 2026-11-08 17:07 UTC ([results](results/2026-11-08-1707-load.md)), on the code of the list preview fix (step 8 of [performance.md](../../docs/performance.md#step-8-the-list-preview)). Earlier full runs: [2026-11-06 22:54 UTC](results/2026-11-06-2254-load.md), with the clock scenarios from [2026-11-07 00:01 UTC](results/2026-11-07-0001-load.md).

| Scenario | What happens | What it measures | Time |
|---|---|---|---|
| `healthy` | No faults. | The send time at the limit, the baseline for the others. | 96 s |
| `default-faults` | The demo's faults: 2% 429s, 3% 5xx, 0.2% lost answers. | Everyday retries. | 156 s |
| `429-10pct` | 10% of calls get a random 429. | The channel pauses for `Retry-After` after each 429, and the limit still holds. | 278 s |
| `5xx-30pct` | 30% of calls get a 5xx. | Retries with backoff and the circuit breaker. Messages may fail after their last attempt. | 150 s |
| `lost-5pct` | 5% of calls are accepted but never answered. | An unknown outcome is retried with the same key and accepted once. Messages may fail. | 146 s |
| `slow-3pct` | 3% of calls answer after 3 to 8 s; the worker stops waiting at 5 s, and the provider still accepts them. | Late answers: the retries must reach the provider as duplicates. Fails if no call outlasted the wait. | 118 s |
| `outage-40s` | The provider is stopped for 40 s at 20 s. | Calls that cannot reach the provider, and the provider's journal across the restart. | 141 s |
| `5xx70-then-ok` | 70% 5xx, then a healthy provider at 150 s. | The error-rate alert opens and closes on each channel. | 252 s |
| `sms401-2workers` | Two workers, and the provider refuses SMS credentials until 180 s. | The credential lock shared by both workers: one probe every 30 s, and its alert opens and closes. | 325 s |
| `both401-paused` | The provider refuses both channels' credentials. The test waits for the campaign to pause, fixes the provider and resumes it. | The pause and the resume, and their events. | 756 s |
| `worker-crash` | The worker is killed until a kill leaves messages mid-send, then started again. | Messages left mid-send are recovered and end once. | 97 s |
| `graceful-restart` | The worker is stopped with SIGTERM, as a deploy does, then started; the API restarts too. | The worker exits by itself within its 30 s grace period and leaves no message mid-send. | 98 s |
| `postgres-down-20s` | PostgreSQL is killed at 25 s and started 20 s later. | Sending waits out the database and recovers from its log, with nothing lost or sent twice. | 122 s |
| `redis-lost` | Redis is killed at 30 s and started empty. | The limiter's 8 s hold after it loses its window keeps the limit. | 108 s |
| `redis-crashed-5x` | Redis is killed five times, 12 s apart, from 20 s, and started again at once without being emptied, as after a crash. | Redis comes back empty, so the hold starts each time. Before Redis ran without snapshots, it came back from an old snapshot and went over 100 ([FINDINGS](FINDINGS.md)). | 138 s |
| `redis-crashed-5x-2workers` | The same with two workers sending. | The hold holds for both workers, which share the limit. | 136 s |
| `redis-flushed-10x` | Redis is flushed 10 times, 2 s apart, from 20 s. | The same hold, again and again. | 128 s |
| `redis-flushed-10x-2workers` | The same with two workers. | The hold holds across workers. | 128 s |
| `sms401-redis-lost` | SMS credentials refused, Redis lost at 60 s, the provider fixed at 150 s. | The credential lock is lost with Redis and set again, and the limit holds. | 277 s |
| `clock-forward-70ms`, `clock-forward-100ms` | The wall clock of Docker's VM, which Redis reads, is stepped forward by 70 or 100 ms five times, 10 s apart, from 20 s. | Steps within the limiter's 100 ms margin let nothing in early. | 96 s, 98 s |
| `clock-forward-150ms`, `clock-forward-500ms` | The same with steps of 150 and 500 ms. | How far a step beyond the margin goes over. The peak is reported, not failed: 104 and 117 in the first run, 103 and 106 in the latest. | 98 s, 99 s |
| `workers-at-120` | The workers' limit is set to 120, above the provider's. | The provider's own limit as the last check: it must refuse some calls, and every message must still be accepted once. Calls over 100 a second are expected here. | 97 s |

## Running

```bash
make verify-up
```

```bash
make verify-security
```

```bash
make verify-load
```

```bash
make verify-mutation
```

```bash
make verify-down
```

The verification stack runs under its own compose project (`scc-verify`), image and ports (API 18081, provider 18083, web 18080), so it can run next to the demo stack. `verify-down` removes only its own containers and volumes. It seeds 116,000 users (set `VERIFY_USERS` to change that) and always uses the mock model, so a run costs nothing whatever `.env` holds.

`make verify-load` also runs `TestTheChecks`, which needs no stack: it feeds the load checks a bad result for each rule and expects each one to be reported. Every run also compares how many messages each channel ended with against the audience the review counted, so a message lost before it was queued is reported.

To run some load scenarios only, name them:

```bash
VERIFY_ONLY=healthy,5xx70-then-ok make verify-load
```

The clock scenarios step the wall clock of Docker's VM with a privileged container. On Docker Desktop every container shares that clock, so a demo stack running at the same time sees the steps too. Docker Desktop sets the clock back later on its own.

`make test` reads every mutation case without breaking anything: the code it breaks must still appear once, and the test it names must still exist (`TestEveryCaseStillPointsAtItsCodeAndTest`). So a refactor that moves the code fails at once instead of at the next mutation run.

`make verify-mutation` changes source files while it runs and puts each one back afterwards, including on Ctrl-C. Don't edit the code while it runs. If a run is killed hard, the next run refuses to start until the file is restored, and `git diff` shows which one.

## Adding to them

- A load scenario is one entry in `scenarios()` in [load/load_test.go](load/load_test.go): the provider's faults, an optional event (kill, flush or restart something at a given second) and what must appear.
- A security probe is one `r.check` line in [security/security_test.go](security/security_test.go). Give each probe a control: show that the valid request works, so a refusal proves the check and not a malformed request.
- A mutation is one entry in [mutation/cases.go](mutation/cases.go): the code to break, its broken form, and the test that should notice. Add one with each new rule worth keeping.
