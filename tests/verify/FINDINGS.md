# Findings

Problems and wrong behaviour that the verification runs found, newest first. Each entry says how it showed up, what caused it and where it stands. Security items still open are listed in [docs/security.md](../../docs/security.md#open-items).

## 2026-11-06

### Fixed

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

- **When the provider refuses both channels' credentials, the campaign takes about 10 minutes to pause.** Until then each channel sends one test call every 30 s. In the run, it paused at 616 s. Once resumed with working credentials, it sent every message, at 100 calls a second or below.
- **After a worker crash, the worker's own record can show more than 100 calls in one second** (109 in one run). The provider counted 100. Messages left mid-send are recorded when they are recovered, so their times bunch up in the record, not at the provider.

### Not explained yet

- **A delivery test failed once and did not fail again in 13 reruns.** Its output was not kept. If it fails again, keep the output.
