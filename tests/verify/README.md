# Verification suites

These suites check the system as a whole, beyond `make test`. Run them before a release or after a change to sending, retries, the rate limiter, Redis, the provider client or anything security-related. Each run saves its results under [results/](results/), so a new run can be compared with the earlier ones. What the runs found is logged in [FINDINGS.md](FINDINGS.md).

| Suite | What it checks | Needs | Time |
|---|---|---|---|
| `load` | Performance, retries and idempotency. It sends a full campaign of about 15,400 messages under provider faults (429s, 5xx, lost answers, slow answers that outlast the worker's wait, an outage, refused credentials), a crashed worker, a worker stopped as a deploy does, PostgreSQL killed for 20 s, lost Redis data and two workers. In every scenario: each message ends in a final state, none is accepted twice, the provider counts each sent message once, no channel goes over 100 calls a second, and the expected alerts, pauses and resumes appear. | The verification stack | About 75 minutes for all 17 scenarios |
| `security` | 19 probes from outside: sessions on every route, webhook signatures and replay, unsubscribe link tampering, hostile uploads, SQL and markup in every field, CSV formulas, oversized bodies, nginx paths, logs and error answers. Known open items are recorded, not failed. | The verification stack | About 3 minutes |
| `mutation` | Whether the tests catch broken code. Each case first runs the tests written for that code on the clean source, where they must run and pass. Then it puts one deliberate fault into the source, expects those tests to fail, and restores the file. | `make test-env-up` and the frontend's `node_modules` | About 2 minutes |

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

`make verify-load` also runs `TestTheChecks`, which needs no stack: it feeds the load checks a bad result for each rule and expects each one to be reported.

To run some load scenarios only, name them:

```bash
VERIFY_ONLY=healthy,5xx70-then-ok make verify-load
```

`make verify-mutation` changes source files while it runs and puts each one back afterwards, including on Ctrl-C. Don't edit the code while it runs. If a run is killed hard, the next run refuses to start until the file is restored, and `git diff` shows which one.

## Adding to them

- A load scenario is one entry in `scenarios()` in [load/load_test.go](load/load_test.go): the provider's faults, an optional event (kill, flush or restart something at a given second) and what must appear.
- A security probe is one `r.check` line in [security/security_test.go](security/security_test.go). Give each probe a control: show that the valid request works, so a refusal proves the check and not a malformed request.
- A mutation is one entry in [mutation/cases.go](mutation/cases.go): the code to break, its broken form, and the test that should notice. Add one with each new rule worth keeping.
