# Findings

Problems and wrong behaviour that the verification runs found, newest first. Each entry says how it showed up, what caused it and where it stands. Security items still open are listed in [docs/security.md](../../docs/security.md#open-items).

## 2026-11-07

### Checked, nothing wrong found

Three load scenarios were added and run once each ([2026-11-06-2212-load](results/2026-11-06-2212-load.md)). In all three every message ended SENT, none was accepted twice, the provider accepted exactly as many as were sent, and neither channel went over 100 calls a second.

- **PostgreSQL killed for 20 s in the middle of a send** (`postgres-down-20s`). It was killed at 25.4 s and answered again at 48.6 s. The send took 112 s against 87 s without faults, about the time the database was gone. No call ended without an answer and no message was retried.
- **The worker stopped as a deploy does, then the API restarted** (`graceful-restart`). On SIGTERM the worker exited by itself with code 0 in 0.4 s, well inside its 30 s grace period, and left no message mid-send. The send took 89 s.
- **3% of calls answered after 3 to 8 s** (`slow-3pct`). The worker stops waiting at 5 s, so 287 calls timed out while the provider still accepted them. Each was retried with its key, and the provider answered all 287 retries as duplicates, so every message was accepted once. The send took 112 s.

### Changed, not yet measured against a clock step

- **The limiter's window is 1.1 s again, for forward steps of Redis's clock.** A call now counts until 1.1 s after its answer instead of 1 s. A step of up to 100 ms, such as the 70.7 ms one measured on 2026-11-06, should then let nothing in early; a larger step only the calls of the part beyond 100 ms. No step happened during the runs below, so this is reasoned, not measured: a run that steps the clock on purpose would show it. A unit test checks that a call still counts a second after its answer and is free 1.2 s after it.

  It costs about 9% of the rate. Five scenarios rerun with the 1.1 s window ([2026-11-06-2233-load](results/2026-11-06-2233-load.md)) all passed at 100 calls a second or below:

  | Scenario | 1 s window | 1.1 s window |
  |---|---|---|
  | healthy | 87.4 s | 96.3 s |
  | default-faults | 156.4 s | 156.3 s |
  | worker-crash | 88.3 s | 97.5 s |
  | redis-flushed-10x-2workers | 119.2 s | 127.8 s |
  | slow-3pct | 111.9 s | 119.1 s |

  With the default faults the send time did not change: the pauses after 429s set the pace there, not the window.

### The provider now enforces its own limit

The mock provider refuses calls over 100 a second per channel with a 429, as a real one does, on top of its random 429s. Its peak still counts every request that reaches it, refused ones included, so the limit is checked on what the workers sent, not on what the provider let through. Outside the over-limit scenario, a single call refused for the limit fails the run.

A full run of all 18 scenarios with the 1.1 s window and the provider's limit on passed ([2026-11-06-2254-load](results/2026-11-06-2254-load.md)). In 17 scenarios no channel went over 100 calls a second and the provider refused nothing for its limit; `sms401-redis-lost`, which reached 102 on 2026-11-06, stayed at 100. In `workers-at-120` the workers' limit was set to 120: the provider saw up to 118 email and 120 SMS calls in a second, refused 1,060 of them, and every message was still accepted once. The send took 98 s, against 97 s without faults.

### Fixed

- **The docs said the mock provider kept its idempotency keys and inbox in memory.** It has written them to a journal on its own volume since the journal was added, and replays it on start. README.md and docs/security.md now say so, and the open item about a restart accepting a message twice is gone.

## 2026-11-06

### Fixed

- **The suites themselves had gaps, found by reading them before a run:**
  - One mutation case named a test that does not exist (`TestDescribe`). Go ran nothing, passed, and the fault counted as not caught for the wrong reason. Each case's tests now run on the clean source first: they must run at least one test and pass, or the case stops there.
  - The load suite restarts the mock provider in five scenarios, and a restart sets its counters back to zero. The provider's peak was read only at the end, so the riskiest moments went unchecked, such as losing Redis before the restart. The suite now keeps the counters from just before each stop and checks the highest peak across all of them.
  - A load scenario whose campaign finished before its event (a Redis flush, an outage) passed without facing the event. Such a run now fails. So does a campaign that had no messages to send.
  - An event that failed made the test fail, but the results table still said "none". The table now lists it.
  - The verification stack read `.env`. With a real model key there, a load run would have paid for every campaign it creates. The stack is now pinned to the mock model.

  A stack-free test (`TestTheChecks`) now feeds the load checks a bad result for each rule and expects each one to be reported.

- **A security probe passed for the wrong reason.** The probe for `javascript:` call-to-action links sent the whole content object it had read from the API. The API refused it for its unknown fields before it checked the link, so the probe passed without testing the link at all. It now sends only the fields a save takes, checks that the same body with an `https` link saves, and then expects the `javascript:` link to be refused as `invalid_link` (422). The API was already right. The other probes were checked for the same mistake. Where a refusal could have come from a malformed request, a control was added: the real unsubscribe link must work, oversized images must be refused for their size, and an uploaded image must be read back before checking its metadata. One more showed up on a second run: the SQL-in-a-name probe saved an audience under a fixed name, so the second run got 409 for a name already taken and wrote nothing. The name is now unique per run, and the probe needs both writes stored.
- **Three unit tests let a broken rule through** (found by the mutation suite, 20 of 23 caught):
  - A repeating schedule cancelled after it had run could show as running. Only a schedule cancelled before its first run was covered.
  - The lock that writes an alert once could be removed. The test raced five checks against each other and usually passed without the lock.
  - A 401 during a breaker probe could count as a passed probe and close the breaker. The test ran with two probes, so the second one hid it.

  All three tests were tightened. All 23 faults are now caught.
- **The error-rate alert stayed shut while 70% of calls failed.** In a full load run, a 70% failure scenario that followed healthy runs never opened the alert. The rule read the channel's last ten minutes, and the healthy calls from the earlier runs outweighed the failures. The rule now reads the channel's last 20 calls within those ten minutes. A unit test covers failures after 500 healthy calls.
- **After Redis lost its data, a channel went over the provider's limit.** Ten `FLUSHALL`s two seconds apart pushed the provider to 189 email and 193 SMS calls in one second. Each worker's sliding window lived only in Redis, so after a loss every worker saw an empty window. A first fix, writing back the answers the workers still knew, made no difference (197 and 199): most answers had already been reported. A channel now waits 8 s after Redis loses its window, longer than any call can still be out. Measured again: 100 and 100, with one worker and with two.
- **Known vulnerabilities in dependencies.** `golang.org/x/net` v0.61.0 had two advisories, in a package this module does not use. It was upgraded to v0.62.0. The frontend image's base, `nginx:1.29-alpine`, had 236 known vulnerabilities, 1 of them critical. It moved to `nginx:1.30-alpine` and now scans clean.

### Behaviour worth knowing, not a bug

- **A forward step of Redis's clock lets a channel go over 100 calls a second.** Narrowed on 2026-11-07 by a 1.1 s window, see above. What follows was measured with the 1 s window. The full load run failed one scenario: email reached 102 calls in one second at the provider. Rerunning the Redis scenarios three times gave 109 and 107 in a plain run, 15 s before Redis was touched, on both channels at the same moment. The worker's own record showed 100 each time.

  The limiter counts the last second by Redis's wall clock (`TIME`). Docker Desktop corrects the VM's clock against the Mac's from time to time. A watcher in the VM compared the wall clock with the monotonic clock every 10 ms during eight healthy runs. It saw one step, 70.7 ms forward at 21:04:39.75 UTC. The one run that went over, to 119 on both channels, had its busiest second end at 21:04:40.47, the window containing that step. The other seven runs stayed at 100. A step forward makes the calls of the last moments look older than they are, so the window lets new calls in early. Permits go out in batches, so more calls can fall in those 70 ms than an even rate would put there. A real server stepping its clock would do the same.

  Results: [2026-11-06-1942-load](results/2026-11-06-1942-load.md) (the full run) and [2026-11-06-2103-load](results/2026-11-06-2103-load.md) (the run during the step).

  Why it was first left as a known limit: it is rare and small. Three of about 26 measured sends went over (102, 109, 119), each for one second; every other second stayed at or below 100. Steps come from something setting the clock at once. Docker Desktop does that to its VM; chrony on a Linux server slews small corrections and steps only after boot or when forced. The extra calls are those granted within the step's span, and the provider answers them with 429, which the worker retries without losing or repeating a message. Other Redis limiters leave this to how the clock is run as well: they take the time from Redis to avoid skew between hosts and do not handle steps of Redis's own clock. The two fixes weighed were a 1.1 s window, about 9% of the rate on every send for steps up to 100 ms, and the workers' monotonic clock, which ties all workers to one host. Neither was worth it for this. The full account is in [errors-and-retries.md](../../docs/errors-and-retries.md#when-rediss-clock-steps-forward).

  If a load run goes over 100 by a few calls in one second while the worker's own record shows 100, check the clock before treating it as a limiter fault.
- **When the provider refuses both channels' credentials, the campaign takes about 10 minutes to pause.** Until then each channel sends one test call every 30 s. In the run, it paused at 616 s. Once resumed with working credentials, it sent every message, at 100 calls a second or below.
- **After a worker crash, the worker's own record can show more than 100 calls in one second** (109 in one run). The provider counted 100. Messages left mid-send are recorded when they are recovered, so their times bunch up in the record, not at the provider.

### Not explained yet

- **A delivery test failed once and did not fail again in 13 reruns.** Its output was not kept. If it fails again, keep the output.
