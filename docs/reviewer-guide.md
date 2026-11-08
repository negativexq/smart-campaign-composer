# Reviewer guide

A short path through the four claims a reviewer should check first. For each one this page says what the claim is, where it is enforced in the code, and which tests prove it, so you can open the code and the test side by side. The reasons behind each choice are in [DECISIONS.md](../DECISIONS.md).

1. [Claude's output is checked, not trusted](#1-claudes-output-is-checked-not-trusted)
2. [Nothing is sent that a person did not approve](#2-nothing-is-sent-that-a-person-did-not-approve)
3. [Rate limiting across workers and failures](#3-rate-limiting-across-workers-and-failures)
4. [A message is accepted once and not lost](#4-a-message-is-accepted-once-and-not-lost)
5. [Beyond these four](#5-beyond-these-four)
6. [Run the checks yourself](#6-run-the-checks-yourself)

## 1. Claude's output is checked, not trusted

**Claim:** the model only proposes. Every audience rule and every message it writes passes the backend's own checks before it is stored, and output that fails is sent back with the reasons or dropped. It never reaches a customer unchecked.

**How:**

- The model answers through a JSON schema. The schema gives the right shape; it cannot check meaning, so the backend checks everything again.
- **Audience.** [`segment.ParseProposal`](../internal/segment/proposal.go#L159) rejects unknown fields, unknown cities and out-of-range windows. [`segment.CheckAgainstBrief`](../internal/segment/grounding.go#L43) then compares the rule with the brief's own words. A city, time, purchase condition or language the brief does not say goes back to the model. A condition no filter can express, such as an age, blocks approval instead of being dropped. Only a validated rule reaches the SQL compiler. The compiler passes every value as a parameter, and an empty rule matches nobody, not everyone.
- **Messages.** [`content.ParseGenerated`](../internal/content/content.go#L217) checks every field against its limits and the brief:
  - the discount and its percent;
  - the link, since the model may not invent one;
  - the discount code, which goes in the SMS and the email's code box, not in the email text;
  - numbers, cities and links the brief does not contain.
  
  Markup cannot get in, because a template stores text in fixed blocks.
- **Repair.** Invalid output gets up to two more attempts, with the validation errors passed back to the model ([`MaxProposalAttempts`](../internal/campaign/service.go#L39) is 3). Refusals, truncation and outages are not retried this way. Output that never validates is not stored. Streamed text is display only, and the screen is cleared if a repair starts over.
- Text in the brief is data. The prompt says so, and the benchmark has 18 injection cases.

**Tests:**

| What | Test |
|---|---|
| Unknown fields, cities and windows rejected | [`TestParseRejectsUntrustedOutput`](../internal/segment/proposal_test.go#L60) |
| A rule that contradicts the brief goes back | [`TestARuleThatContradictsTheBriefIsRejected`](../internal/segment/grounding_test.go#L75) |
| An empty rule matches nobody, values are parameters | [`TestEmptySpecMatchesNobody`](../internal/segment/compile_test.go#L188), [`TestCompilePassesEveryValueAsParameter`](../internal/segment/compile_test.go#L38) |
| Message shapes and discounts | [`TestParseGeneratedRejectsUntrustedShapes`](../internal/content/content_test.go#L56), [`TestDiscountsMustMatchTheBrief`](../internal/content/content_test.go#L179) |
| A link the model typed or made up | [`TestAModelThatTypesTheLinkOrMakesOneUpIsHeldToTheRules`](../internal/content/link_test.go#L108) |
| The code stays out of the email text | [`TestAWrittenEmailLeavesTheCodeToItsBox`](../internal/content/email_code_test.go#L12) |
| Repair with the errors, then give up and store nothing | [`TestRepairRetriesWithTheValidationError`](../internal/campaign/service_test.go#L174), [`TestRepairGivesUpAndStoresNothing`](../internal/campaign/service_test.go#L204), [`TestInvalidContentIsNeverStored`](../internal/campaign/generate_test.go#L244) |
| The Claude adapter sends errors back | [`TestAnthropicSendsValidationErrorsBackForRepair`](../internal/ai/anthropic_test.go#L124) |

**Against a real model:** [`tests/benchmark`](../tests/benchmark) holds 247 hard briefs, refinements and content instructions with expected answers. The latest full Claude Haiku 5.5 run, on 2026-11-09, had 243 as expected, 2 blocked without need, 2 shown as unsure, and 0 silently wrong audiences or messages with forbidden text ([report](../tests/benchmark/results/2026-11-09-0220-anthropic-claude-haiku-5-5.md)). Older reports in that folder ran on an earlier prompt, schema or case set, and some show silently wrong answers that were fixed since. Each says so in its first line, and the [results index](../tests/benchmark/results/README.md) lists which report is current for each model. The 32 gaps the backend has without a model are listed in [`known_gaps.txt`](../tests/benchmark/known_gaps.txt). `make test` fails on any new gap and on any listed gap that closed.

## 2. Nothing is sent that a person did not approve

**Claim:** valid model output is only a draft. A person approves one specific version, and what goes out is that version: its audience rule and its messages in every language. Neither can change afterwards. The audience snapshot taken at approval is frozen too, but it is not the number sent: consent is checked again before each message, so fewer can go out, and each run of a repeating campaign finds who matches the frozen rule at that time.

The approver is the name typed at sign-in. It records who did what; it is not authentication. See [A simple sign-in that says who, not who you really are](../DECISIONS.md#a-simple-sign-in-that-says-who-not-who-you-really-are).

**How:**

- [`Service.ApproveAt`](../internal/campaign/dispatch.go#L93) locks the campaign row and refuses an approval in each of these cases:
  - the campaign is not ready for review;
  - the approval carries no review revision;
  - the revision differs from the current one, because the messages, audience or languages changed after the page loaded;
  - something still blocks the audience;
  - a language has no messages.
- On the review page, the approve button stays off until every language and every A/B version has been opened.
- Approval stores the audience snapshot, its counts and step-by-step breakdown, with the version. What was really sent is a separate figure, read from the delivery records.
- A database trigger, [`campaign_versions_immutable_after_approval`](../migrations/00001_initial_schema.sql#L77), rejects any update or delete of an approved version. The rule holds even if application code has a bug.
- Sending and scheduling need an approved version. A one-time send goes to the people who were approved. Each later run of a schedule sends that same version to whoever matches its rule then. Consent is checked again before each message either way, so a withdrawal after approval stops it.

**Tests:**

| What | Test |
|---|---|
| The database refuses to change an approved version | [`TestApprovedVersionIsImmutable`](../internal/db/schema_test.go#L242) |
| Approval freezes the version and its audience snapshot | [`TestApprovalFreezesTheVersion`](../internal/campaign/dispatch_test.go#L54), [`TestApprovalFreezesTheAudienceCounts`](../internal/campaign/dispatch_test.go#L1144) |
| An approval without a revision, or of an older review | [`TestAnApprovalWithoutARevisionIsRefused`](../internal/campaign/dispatch_test.go#L1327), [`TestAnApprovalOfAnOlderReviewIsRefused`](../internal/campaign/dispatch_test.go#L1253) |
| Only a reviewed campaign, with every language | [`TestOnlyAReviewedCampaignCanBeApproved`](../internal/campaign/dispatch_test.go#L100), [`TestApprovalNeedsAVersionForEveryLanguage`](../internal/campaign/dispatch_test.go#L1190) |
| Sending needs approval | [`TestDispatchNeedsApproval`](../internal/campaign/dispatch_test.go#L145), [`TestSchedulingNeedsApprovalAndAValidRule`](../internal/campaign/dispatch_test.go#L356) |
| Messages written for an earlier code or link | [`TestMessagesWrittenForAnEarlierCodeCannotBeApproved`](../internal/campaign/code_test.go#L192), [`TestMessagesWrittenForAnEarlierLinkCannotBeApproved`](../internal/campaign/link_test.go#L101) |
| Consent withdrawn after approval | [`TestConsentWithdrawnAfterApprovalStillStopsTheMessage`](../internal/campaign/dispatch_test.go#L914) |

**The limit:** opening a version shows it to the person; it does not prove they read it. See [Approval is slower on purpose](../DECISIONS.md#approval-is-slower-on-purpose).

## 3. Rate limiting across workers and failures

**Claim:** each channel stays at or below 100 calls a second as the provider counts them. This holds for any number of workers, through retries, crashes and the loss of Redis, and through forward steps of Redis's clock of up to 100 ms. Larger clock steps can let a few calls through for a second; the measured limit is under [Under load](#under-load).

**How:**

- **Sliding window.** A Lua script in Redis keeps a sliding window per channel ([`limiter.go`](../internal/delivery/limiter.go#L78)). A fixed one-second bucket would allow up to 200 across its edge.
- **Permission first.** A worker asks for permission before it claims messages from the PostgreSQL queue, so a channel with no permission holds nothing. After claiming, it settles its own reservation, and unused permits go back.
- **How long a call counts.** A call counts from when it goes out until 1.1 s after its answer ([`grantWindow`](../internal/delivery/limiter.go#L28)). A call held up on the way therefore cannot land in the same second as the next batch, and the extra 100 ms covers a forward step of Redis's clock.
- **A call with no answer.** It counts for the send timeout plus 2 s, because it may still arrive.
- **Redis loses its window.** After a restart, flush or eviction, the channel waits 8 s before granting anything. Redis runs without snapshots, so an old window cannot come back after a crash.
- **Redis is down.** Nothing is sent, and no attempt is used.

**Tests:**

| What | Test |
|---|---|
| The rate holds across workers | [`TestLimiterHoldsTheRateAcrossWorkers`](../internal/delivery/limiter_test.go#L17), [`TestProviderNeverSeesMoreThanTheLimitAcrossWorkers`](../internal/delivery/limiter_test.go#L220) |
| A call counts until after its answer, or for its hold | [`TestACallCountsUntilTheWindowAfterItsAnswer`](../internal/delivery/limiter_test.go#L301), [`TestACallWithNoAnswerCountsForItsHold`](../internal/delivery/limiter_test.go#L337) |
| One worker cannot erase another's permits | [`TestSettlingCannotEraseAnotherWorkersPermits`](../internal/delivery/limiter_test.go#L103) |
| The hold after Redis loses its window | [`TestAChannelWaitsAfterRedisLostItsWindow`](../internal/delivery/limiter_test.go#L153) |
| Nothing goes out without Redis | [`TestLimiterFailsWhenRedisIsDown`](../internal/delivery/limiter_test.go#L370), [`TestNothingIsSentWithoutTheRateLimiter`](../tests/chaos/chaos_test.go#L277) |

### Under load

The [24 load scenarios](../tests/verify/README.md#load-scenarios) send about 15,400 messages each. They add provider faults, slow and lost answers, an outage, crashed and restarted workers, PostgreSQL killed, Redis flushed and crashed, and Redis's clock stepped forward. The mock provider counts every call on arrival, refused ones included, and any second over 100 fails the run, outside the scenarios that test past the limit on purpose. Clock steps of 70 and 100 ms stayed at 100. Larger steps are reported rather than failed: 104 after 150 ms, and 117 after 500 ms. The provider refuses those extra calls, and each message is still accepted once. The trade-off is explained in [DECISIONS.md](../DECISIONS.md#the-rate-limiter-is-a-sliding-window-in-redis).

Two mutation cases break the limiter on purpose: one removes the clock margin, the other skips the hold after Redis loses its window. The tests have to catch both ([`mutation/cases.go`](../tests/verify/mutation/cases.go)).

## 4. A message is accepted once and not lost

**Claim:** each message the campaign plans is accepted by the provider at most once, through retries, lost answers, crashed workers and restarts of the provider, the database and Redis. A message that cannot be settled is kept and marked, not dropped. When the outcome cannot be known, it says `UNKNOWN` instead of guessing `FAILED`.

**How:**

- **One delivery per message.** The database allows one delivery per run, recipient and channel ([`UNIQUE (run_id, user_id, channel)`](../migrations/00027_campaign_runs.sql#L42)), and each delivery has its own idempotency key, sent with every retry of it.
- **The provider remembers the key.** The mock writes each accepted key to a journal on its own volume before it answers ([`journal.add`](../internal/mockprovider/journal.go#L82) from [`Server.accept`](../internal/mockprovider/mockprovider.go#L337)) and replays the journal when it starts, so a retry after a restart is recognised as the same message.
- **A crashed worker's messages come back.** A message being sent holds a lease. [`RecoverExpired`](../internal/delivery/worker.go#L1153) takes back messages whose lease ran out, with the same key, and counts the lost call as an attempt, so a message that keeps crashing workers ends instead of looping.
- **No answer is not a failure.** A call that timed out may have gone through, so a message that runs out of attempts after one ends as `UNKNOWN`.
- **The queue is the database.** Claiming, sending state and attempts change in one transaction, so there is no second store to fall out of step with.

**Tests:**

| What | Test |
|---|---|
| One delivery per recipient and channel, unique keys | [`TestDeliveryAllowsOnePerRecipientAndChannel`](../internal/db/schema_test.go#L134), [`TestDeliveryIdempotencyKeyIsUnique`](../internal/db/schema_test.go#L186) |
| A retry after a provider restart is the same message | [`TestARetryAfterARestartIsNotANewMessage`](../internal/mockprovider/journal_test.go#L34), [`TestALostResponseAndARestartDeliverOnce`](../internal/mockprovider/journal_test.go#L56) |
| A crashed worker's messages are sent again, once, with their key | [`TestMessagesLeftByACrashedWorkerAreSentAgain`](../internal/delivery/worker_test.go#L923), [`TestARecoveredMessageKeepsItsIdempotencyKey`](../internal/delivery/worker_test.go#L993), [`TestParallelRecoveryCountsEachMessageOnce`](../internal/delivery/worker_test.go#L1016) |
| A timeout ends as unknown, not failed | [`TestTimeoutsEndAsUnknownNotFailed`](../internal/delivery/worker_test.go#L688), [`TestALostResponseIsNotCalledNotSent`](../internal/delivery/client_test.go#L25) |
| A killed or stopped worker, a provider outage, a database outage | [`tests/chaos`](../tests/chaos/chaos_test.go#L241), against a running stack |
| Repeated and reordered delivery reports | [`TestReportsThatRepeatOrArriveOutOfOrderGiveTheSameResult`](../internal/campaign/events_test.go#L482) |

**Under load:** every one of the [24 load scenarios](../tests/verify/README.md#load-scenarios) checks that no delivery has two accepted attempts and that the provider accepted no more messages than the campaign counts as sent.

**The limit:** "at most once" holds only as long as the provider honours the key. The mock does; a real provider has to be checked for it. See [Retrying depends on the provider's idempotency](../DECISIONS.md#retrying-depends-on-the-providers-idempotency).

## 5. Beyond these four

- [Errors and retries](errors-and-retries.md): backoff, the 429 pause, the 401 credential lock, the circuit breaker and channel alerts, with measured behaviour.
- [State machines](state-machines.md): every state and what guards each change.
- [Security](security.md): 19 probes from outside a running stack, scans in CI, and the open items.
- [Mutation check](../tests/verify/mutation/cases.go): deliberate faults the tests must catch.
- [Performance profile](performance.md): a measured baseline and each optimization step measured the same way.
- [FINDINGS.md](../tests/verify/FINDINGS.md): what the verification suites found and what was fixed.

## 6. Run the checks yourself

```bash
make test        # Go and frontend tests and the type check in containers, with the benchmark and mutation case checks that need no model
```

```bash
make up && make test-e2e && make test-chaos
```

```bash
make verify-up && make verify-load && make verify-down   # about 100 minutes
```

`make benchmark` runs the 247 cases through a real model. It needs `LLM_PROVIDER` and a key, and it costs money.
