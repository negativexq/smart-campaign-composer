# Security

This page covers what protects the system, the checks that were run against it, what they found and what is still open. It describes this repository as a demo of the product. It is not a security policy for a production deployment.

## What protects what

| Area | How it is handled | Where |
|---|---|---|
| Who did what | Signing in takes a name with no password and stores it in an unsigned `HttpOnly`, `SameSite=Lax` cookie. Every campaign route answers 401 without it, and the audit trail records the name. This identifies people for the record. It does not authenticate them (see [Open items](#open-items)). | `internal/httpapi/session.go`, [DECISIONS.md](../DECISIONS.md#a-simple-sign-in-that-says-who-not-who-you-really-are) |
| Model output | The model's answers are treated as untrusted input. An audience proposal must pass `segment.ParseProposal`: unknown fields, unknown cities and out-of-range values are rejected, and an empty rule compiles to `FALSE`, never to everyone. Messages are checked for invented offers, numbers and cities. | `internal/segment`, `internal/content` |
| SQL | Values reach PostgreSQL as parameters. The segment compiler builds only fixed fragments, and its values stay parameters. | `internal/segment`, `internal/campaign` |
| Email markup | The model and the marketer write text only. The backend places it in fixed templates with `html/template`, so neither can add markup or script. | `internal/content` |
| Uploaded images | The upload's content is checked, so only PNG and JPEG pass, whatever the file name says. Size, side length and pixel count are capped before decoding. The image is then drawn again from its pixels, so no metadata or hidden content is kept. Drawing one can take a few hundred MB, so two uploads are drawn at a time; the rest wait up to 10 s and are then refused with 503 and `Retry-After`, so a burst of uploads cannot exhaust the API's memory. | `internal/asset`, `internal/httpapi/imageturns.go` |
| Request size | The API caps JSON bodies at 64 KiB and uploads at 5 MiB. nginx caps any request body at 6 MiB in front of it. | `internal/httpapi/api.go`, `frontend/nginx.conf` |
| Unsubscribe links | Each link carries an HMAC-SHA256 of the person's id, so it cannot be changed to withdraw someone else's consent. Opening the link only asks; a POST withdraws consent. The worker does not start without the secret. | `internal/unsubscribe` |
| Provider events | The webhook has no session. Each event must carry an HMAC-SHA256 of its body, compared in constant time. | `internal/httpapi/events.go` |
| Exports | CSV cells that start with `=`, `+`, `-`, `@`, a tab or a carriage return are escaped, so a spreadsheet does not run customer text as a formula. | `internal/httpapi/export.go` |
| Consent | Consent is checked when deliveries are queued and again before each message. | `internal/delivery` |
| The provider's limit | Each channel stays at or below 100 calls a second across all workers, including after Redis loses its data, as long as Redis's clock does not step forward by more than 100 ms; a 500 ms step was measured to let in up to 17 extra calls for one second. The steps seen came from Docker Desktop correcting its VM's clock or were made on purpose; a Linux server whose time service slews is not expected to step during a send. See [errors-and-retries.md](errors-and-retries.md#how-calls-count-against-the-limit), and [when the clock steps](errors-and-retries.md#when-rediss-clock-steps-forward). | `internal/delivery/limiter.go` |
| Refused credentials | A 401 from the provider uses no attempt. It locks the channel for every worker, and one call every 30 s tests the key. | `internal/delivery`, [errors-and-retries.md](errors-and-retries.md#401-the-provider-refuses-the-credentials) |
| Telemetry | Metrics carry no campaign, user, email, phone, brief or message labels. | `internal/telemetry` |
| Secrets | API keys and signing secrets come from environment variables. They are not stored in the repository or the database. | `compose.yaml`, `cmd/` |
| Scans on every change | CI fails when Trivy finds a secret in the files git tracks, or a HIGH or CRITICAL vulnerability with a fix available in the backend or frontend image. Ignored files such as `.env` are not sent to the scan. | `Makefile` (`scan-secrets`, `scan-images`), `.github/workflows/ci.yml` |

## Checks run on 2026-11-06

All checks ran against the working tree that includes the channel alerts, the credential lock, the Redis-loss hold and scheduling again.

| Check | Result |
|---|---|
| `gofmt`, `go vet ./...`, `go vet -tags=e2e,chaos,benchmark ./tests/...` | Clean |
| `go test -race -count=1 ./...` | All 16 packages pass |
| Frontend `npm test` and `npm run build` (type check included) | 352 tests pass, build clean |
| `make test-e2e` on a separate stack | Passes |
| `make test-chaos` on a separate stack | 5 of 5 pass: a killed worker, a stopped worker, sending without the rate limiter, a provider outage and a database outage. Each run sent all 869 messages, with none lost or sent twice. |
| `npm audit` | 0 vulnerabilities |
| `trivy fs` (vulnerabilities, secrets, misconfiguration) | 2 vulnerabilities in a Go dependency, fixed (see below). No secrets. Two Dockerfile warnings (see [Open items](#open-items)). |
| `trivy image` on the backend image (Alpine and the four Go binaries) | 0 vulnerabilities |
| `trivy image` on the frontend image | 236 before the fix below, 0 after |
| Manual review of the new code | No string-built SQL, no `v-html`, and the schedule routes stay behind the session check. The Lua scripts only receive numbers and channel names from configuration. |

### Do the tests catch broken code?

A passing test only helps if it fails when the code it covers breaks. To check that, 23 deliberate faults were put into the new code one at a time, each followed by the tests written for it. Examples: letting a cancelled schedule never show as stopped, writing an alert without its lock, letting a 401 count as a healthy breaker probe, dropping the Redis-loss hold, and hiding the "Schedule again" button.

The first pass caught 20 of the 23. The three misses were weak tests, not broken code:

- **A repeating schedule cancelled after it ran** was not covered, only one cancelled before its first run. The repeating-schedule test now cancels after two runs and checks that the rule shows as stopped.
- **The alert lock** was tested by five checks racing each other, which usually passed without the lock too. The test now holds the lock itself, checks that the worker waits for it, and checks that the alert is written once.
- **A 401 during a breaker probe** went through the test either way, because without its own handling it counted as a passed probe and closed the breaker. The test now checks that the breaker stays open after the 401 and closes only after an accepted call.

After these changes all 23 faults are caught. A 24th was added with the 1.1 s limiter window: setting it back to 1 s must fail its test. The latest run, on 2026-11-08, catches all 24. Two cases had to be pointed at code that had moved since, a query rewritten and a label moved to its own file, because the check refuses to run when the code it breaks is not found.

### Probes from outside and full sends under faults

The verification suites in [tests/verify](../tests/verify/README.md) run against a stack of their own and keep their results.

**Security probes** ([2026-11-06-1939-security](../tests/verify/results/2026-11-06-1939-security.md)): 19 of 19 pass. Each probe has a control, a valid request that must work, so a refusal shows the check and not a malformed request.

| Area | What was probed |
|---|---|
| Sessions | All 46 routes refuse four kinds of missing or bad cookie. |
| Provider webhook | Missing, wrong, malformed and other-body signatures get 401; a replayed event changes nothing; unknown keys and events and a 70 KB body are refused. |
| Unsubscribe links | Five altered links get 404, the real one works. |
| Uploads | Non-images, SVG with script, HTML named `.png`, empty and 7 MB files are refused; decompression bombs are refused by their header size; file names cannot climb directories; uploaded images lose their metadata. |
| Injection | SQL in 200 query strings and paths and in written names is stored as data, with the tables intact; markup in email content is escaped; a `javascript:` call-to-action link is refused. |
| Exports | CSV cells that start a formula are escaped. |
| Request size | 2 MB, deeply nested and broken bodies are refused. |
| nginx | Six paths outside the app serve nothing. |
| Logs and errors | 23,384 log lines held no email address, phone number or secret; error answers reveal no internals. |

Two known items are recorded rather than failed: the frontend sends no security headers, and the API's `/metrics` answers without a session (see [Open items](#open-items)).

**Full sends under faults** ([latest full run](../tests/verify/results/2026-11-06-2254-load.md)): each scenario sends the same campaign of 15,411 messages. In the latest runs of all 24 scenarios no message was lost or accepted twice, and outside the scenarios that go over on purpose no channel went over 100 calls a second at the provider. They cover provider faults and outages, refused credentials, crashed and restarted workers, PostgreSQL and Redis lost, Redis's clock stepped forward, and workers set above the provider's limit. Two problems they found were fixed: Redis came back from an old snapshot after a crash and let a channel reach 123 calls in a second, and a forward clock step let one reach 119. The details are in [FINDINGS.md](../tests/verify/FINDINGS.md).

### Findings fixed

- **`golang.org/x/net` v0.61.0** had two advisories, CVE-2026-78661 and CVE-2026-97045. Both are in `x/net/html`, which this module does not use: `go mod why` shows that only `x/net/trace` comes in, through gRPC. It was upgraded to v0.62.0, which fixes both.
- **The frontend image was built on `nginx:1.29-alpine`**, a tag last updated in April 2026. Its Alpine packages had 236 known vulnerabilities, 1 critical and 52 high. The image now uses `nginx:1.30-alpine`, the maintained stable line. The configuration passes `nginx -t`, and the image scans clean.

## Open items

These were found or already known, and are left as they are for now:

- **Sign-in is not authentication.** Anyone can type any name or edit the cookie. A real login would replace the cookie handling in `internal/httpapi/session.go`.
- **The frontend container runs nginx as root** (trivy `DS-0002`). Moving to an unprivileged user means nginx listens on a port above 1024, and the compose mapping changes with it.
- **nginx sets no security headers** such as `Content-Security-Policy`, `X-Content-Type-Options` or `X-Frame-Options`.
- **There is no request rate limit on the API.**
- **Development secrets have defaults in `compose.yaml`**: `UNSUBSCRIBE_SECRET`, `PROVIDER_WEBHOOK_SECRET` and the PostgreSQL password. Any shared or public deployment has to set its own.
- **Published ports listen on every interface.** On a shared network, the API, worker and mock provider ports are reachable from other machines.
- **Dockerfiles have no `HEALTHCHECK`** (trivy `DS-0026`). Compose defines the health checks instead.

## Running the checks

```bash
gofmt -l . && go vet ./... && go vet -tags=e2e,chaos,benchmark ./tests/...
```

```bash
make test-local
```

```bash
cd frontend && npm test && npm run build && npm audit
```

```bash
trivy fs --scanners vuln,secret,misconfig --skip-dirs frontend/node_modules .
```

The scans CI runs, with Trivy in a container, so only Docker is needed:

```bash
make scan
```

```bash
make up && make test-e2e && make test-chaos
```

The security probes, the load scenarios and the mutation check are kept in [tests/verify](../tests/verify/README.md), with their results:

```bash
make verify-up && make verify-security && make verify-load && make verify-mutation && make verify-down
```

The chaos tests run their own `docker compose` commands. To run them against a stack under another project name, export `COMPOSE_PROJECT_NAME` for the whole run, not only for `make up`.
