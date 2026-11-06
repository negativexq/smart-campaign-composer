# Findings

Problems and wrong behaviour that the verification runs found, newest first. Each entry says how it showed up, what caused it and where it stands. Security items still open are listed in [docs/security.md](../../docs/security.md#open-items).

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

- **A forward step of Redis's clock lets a channel go over 100 calls a second. A known limit, not fixed.** The full load run failed one scenario: email reached 102 calls in one second at the provider. Rerunning the Redis scenarios three times gave 109 and 107 in a plain run, 15 s before Redis was touched, on both channels at the same moment. The worker's own record showed 100 each time.

  The limiter counts the last second by Redis's wall clock (`TIME`). Docker Desktop corrects the VM's clock against the Mac's from time to time. A watcher in the VM compared the wall clock with the monotonic clock every 10 ms during eight healthy runs. It saw one step, 70.7 ms forward at 21:04:39.75 UTC. The one run that went over, to 119 on both channels, had its busiest second end at 21:04:40.47, the window containing that step. The other seven runs stayed at 100. A step forward makes the calls of the last moments look older than they are, so the window lets new calls in early. Permits go out in batches, so more calls can fall in those 70 ms than an even rate would put there. A real server stepping its clock would do the same.

  Results: [2026-11-06-1942-load](results/2026-11-06-1942-load.md) (the full run) and [2026-11-06-2103-load](results/2026-11-06-2103-load.md) (the run during the step).

  Why it stays: it is rare and small. Three of about 26 measured sends went over (102, 109, 119), each for one second; every other second stayed at or below 100. Steps come from something setting the clock at once. Docker Desktop does that to its VM; chrony on a Linux server slews small corrections and steps only after boot or when forced. The extra calls are those granted within the step's span, and the provider answers them with 429, which the worker retries without losing or repeating a message. Other Redis limiters leave this to how the clock is run as well: they take the time from Redis to avoid skew between hosts and do not handle steps of Redis's own clock. The two fixes weighed were a 1.1 s window, about 9% of the rate on every send for steps up to 100 ms, and the workers' monotonic clock, which ties all workers to one host. Neither was worth it for this. The full account is in [errors-and-retries.md](../../docs/errors-and-retries.md#when-rediss-clock-steps-forward).

  If a load run goes over 100 by a few calls in one second while the worker's own record shows 100, check the clock before treating it as a limiter fault.
- **When the provider refuses both channels' credentials, the campaign takes about 10 minutes to pause.** Until then each channel sends one test call every 30 s. In the run, it paused at 616 s. Once resumed with working credentials, it sent every message, at 100 calls a second or below.
- **After a worker crash, the worker's own record can show more than 100 calls in one second** (109 in one run). The provider counted 100. Messages left mid-send are recorded when they are recovered, so their times bunch up in the record, not at the provider.

### Not explained yet

- **A delivery test failed once and did not fail again in 13 reruns.** Its output was not kept. If it fails again, keep the output.
