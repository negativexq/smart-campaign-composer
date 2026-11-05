//go:build load

// Package load_test measures how delivery behaves under load and failure, seen from
// the provider: real worker processes, real Postgres and Redis, real HTTP. A broken
// rule fails its scenario. Throughput is only recorded, as there is no fixed target.
package load_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// usualLatency is how long the provider takes in the demo: 10 to 60 ms.
func usualLatency(seed uint64) func() time.Duration {
	return randomDelay(seed, 10*time.Millisecond, 60*time.Millisecond)
}

func randomDelay(seed uint64, least, most time.Duration) func() time.Duration {
	var mu sync.Mutex
	random := rand.New(rand.NewPCG(seed, 7)) //nolint:gosec // test timing
	return func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return least + time.Duration(random.Int64N(int64(most-least)))
	}
}

func TestT01SteadyCapacity(t *testing.T) {
	count := scaled(10_000)
	r := newRun(t, map[string]int{"sms": count, "email": count})
	r.provider.latency = usualLatency(1)
	r.startWorkers(1)
	status := r.waitUntilDone()
	record(t, r.finish("T01 steady capacity", "What rate holds for a long send with no provider trouble?",
		fmt.Sprintf("One worker, %d messages on each channel, no faults, provider answers in 10-60 ms.", count), status))
}

// TestT03OneRateLimit compares a single 429 with no 429 at all. Calls go out in groups
// of 100, so call 500 ends a group and call 550 falls in the middle of one.
func TestT03OneRateLimit(t *testing.T) {
	for _, at := range []int{0, 500, 550} {
		t.Run(fmt.Sprintf("call%d", at), func(t *testing.T) {
			count := scaled(2_000)
			r := newRun(t, map[string]int{"email": count})
			r.provider.latency = usualLatency(3)
			r.provider.decide = func(_ string, call int) string {
				if call == at {
					return answer429
				}
				return answerAccept
			}
			r.startWorkers(1)
			status := r.waitUntilDone()
			out := r.finish(fmt.Sprintf("T03 429 at call %d", at), "What does a single 429 with Retry-After: 1 cost?",
				fmt.Sprintf("One worker, %d emails, call %d answers 429 (0 means none), everything else succeeds.", count, at), status)
			email := out.Channels["email"]
			out.Notes = append(out.Notes, fmt.Sprintf("First to last call: %.2f s. Longest gap: %.2f s.", email.SpanSeconds, email.LongestGap))
			if email.AttemptsUsed != count {
				out.Violations = append(out.Violations, fmt.Sprintf("a 429 used an attempt: %d attempts for %d messages", email.AttemptsUsed, count))
			}
			record(t, out)
		})
	}
}

// TestT01bWorkerCapacity gives the workers a limit they cannot reach, so the rate shows
// what they can do rather than what they are allowed.
func TestT01bWorkerCapacity(t *testing.T) {
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("workers%d", workers), func(t *testing.T) {
			count := scaled(5_000)
			r := newRun(t, map[string]int{"sms": count, "email": count})
			r.limit = 100_000
			r.provider.latency = usualLatency(1)
			r.startWorkers(workers)
			status := r.waitUntilDone()
			record(t, r.finish(fmt.Sprintf("T01b capacity, %d workers", workers), "How fast can the workers go with the limit out of the way?",
				fmt.Sprintf("%d workers, %d messages on each channel, a limit of %d, provider answers in 10-60 ms.", workers, count, r.limit), status))
		})
	}
}

func TestT06NetworkJitter(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			count := scaled(10_000)
			r := newRun(t, map[string]int{"email": count})
			r.provider.latency = usualLatency(seed)
			r.provider.network = randomDelay(seed+100, 0, 200*time.Millisecond)
			r.startWorkers(4)
			status := r.waitUntilDone()
			record(t, r.finish(fmt.Sprintf("T06 jitter, seed %d", seed), "Does the limit hold when calls take 0-200 ms to reach the provider?",
				fmt.Sprintf("Four workers, %d emails, each call delayed 0-200 ms before it counts as arrived.", count), status))
		})
	}
}

// TestT06bNetworkClears is the hard case for the margin: one group of 100 calls is held
// up on the way and the next group is not, so the two land closer than they were granted.
func TestT06bNetworkClears(t *testing.T) {
	for _, delay := range []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 150 * time.Millisecond, 200 * time.Millisecond, 250 * time.Millisecond, 300 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			count := scaled(2_000)
			r := newRun(t, map[string]int{"email": count})
			r.provider.latency = usualLatency(6)
			var (
				mu    sync.Mutex
				calls int
			)
			r.provider.network = func() time.Duration {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if (calls-1)/limit%2 == 0 {
					return delay
				}
				return 0
			}
			r.watchPermits()
			r.startWorkers(4)
			status := r.waitUntilDone()
			record(t, r.finish(fmt.Sprintf("T06b network clears, %s", delay), "Does the limit hold when one group of calls is delayed and the next is not?",
				fmt.Sprintf("Four workers, %d emails, every other 100 calls delayed %s on the way to the provider.", count, delay), status))
		})
	}
}

func TestT07WorkerCounts(t *testing.T) {
	for _, workers := range []int{1, 2, 4} {
		t.Run(fmt.Sprintf("workers%d", workers), func(t *testing.T) {
			count := scaled(3_000)
			r := newRun(t, map[string]int{"sms": count, "email": count})
			r.provider.latency = usualLatency(7)
			r.startWorkers(workers)
			status := r.waitUntilDone()
			record(t, r.finish(fmt.Sprintf("T07 %d workers", workers), "Do more workers share the limit, and change the rate?",
				fmt.Sprintf("%d workers, %d messages on each channel, no faults.", workers, count), status))
		})
	}
}

func TestT09LostResponsesAndAKilledWorker(t *testing.T) {
	count := scaled(3_000)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(9)
	r.provider.decide = func(_ string, call int) string {
		if call%50 == 0 {
			return answerLost
		}
		return answerAccept
	}
	r.startWorkers(2)
	killAt := count / 3
	for r.provider.callCount("email") < killAt {
		time.Sleep(20 * time.Millisecond)
	}
	r.kill(0)
	killed := time.Now()
	status := r.waitUntilDone()
	out := r.finish("T09 lost responses and a killed worker", "Does anyone get a message twice, and does anything get lost?",
		fmt.Sprintf("Two workers, %d emails, every 50th call is accepted with no answer, one worker gets SIGKILL after %d calls.", count, killAt), status)
	out.Notes = append(out.Notes, fmt.Sprintf("Finished %.0f s after the kill. The lease is 60 s.", time.Since(killed).Seconds()),
		fmt.Sprintf("Repeats the provider recognised by key: %d. Calls whose caller was gone before they were handled: %d.", out.KeyRepeats, out.Abandoned))
	var recovered int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM campaign_deliveries WHERE campaign_id = $1 AND last_error = 'lease_expired'`, r.campaign).Scan(&recovered); err == nil {
		out.Notes = append(out.Notes, fmt.Sprintf("Still marked as recovered from a lease at the end: %d.", recovered))
	}
	record(t, out)
}

// lostThenRestart has the provider accept 30 messages without answering, then lose
// its memory of keys before their retries come, unless told to keep it.
func lostThenRestart(t *testing.T, name string, restart bool) {
	count := scaled(600)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(10)
	r.provider.decide = func(_ string, call int) string {
		if call <= 300 && call%10 == 0 {
			return answerLost
		}
		return answerAccept
	}
	r.startWorkers(1)
	if restart {
		for r.provider.callCount("email") < min(320, count/2) {
			time.Sleep(5 * time.Millisecond)
		}
		r.provider.restart()
	}
	status := r.waitUntilDone()
	setup := fmt.Sprintf("One worker, %d emails, 30 accepted with no answer. The worker's send timeout is 5 s, so retries come after this point.", count)
	if restart {
		setup += " The provider forgets every key after 320 calls."
	}
	out := r.finish(name, "Is one message per person kept when a response is lost?", setup, status)
	out.Notes = append(out.Notes, fmt.Sprintf("Repeats the provider recognised by key: %d.", out.KeyRepeats))
	record(t, out)
}

func TestT10aLostResponseProviderRemembers(t *testing.T) {
	lostThenRestart(t, "T10a lost responses, provider remembers", false)
}

func TestT10bLostResponseProviderRestarts(t *testing.T) {
	lostThenRestart(t, "T10b lost responses, provider restarts", true)
}

// TestT02UsualFaults uses a fixed schedule, so limiters face the same faults.
func TestT02UsualFaults(t *testing.T) {
	count := scaled(3_000)
	r := newRun(t, map[string]int{"sms": count, "email": count})
	r.provider.latency = usualLatency(2)
	r.provider.decide = func(_ string, call int) string {
		switch {
		case call%50 == 0:
			return answer429
		case call%33 == 0:
			return answer500
		}
		return answerAccept
	}
	r.startWorkers(1)
	status := r.waitUntilDone()
	record(t, r.finish("T02 usual faults", "How does a send recover under the demo's fault rates?",
		fmt.Sprintf("One worker, %d messages on each channel, every 50th call 429 and every 33rd 500.", count), status))
}

func TestT04TenRateLimitsInARow(t *testing.T) {
	count := scaled(2_000)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(4)
	r.provider.decide = func(_ string, call int) string {
		if call > 500 && call <= 510 {
			return answer429
		}
		return answerAccept
	}
	r.startWorkers(1)
	status := r.waitUntilDone()
	out := r.finish("T04 ten 429 in a row", "Do repeated 429s extend the pause correctly and cost no attempts?",
		fmt.Sprintf("One worker, %d emails, calls 501 to 510 answer 429.", count), status)
	email := out.Channels["email"]
	out.Notes = append(out.Notes, fmt.Sprintf("Longest gap %.2f s; first to last call %.2f s.", email.LongestGap, email.SpanSeconds))
	if email.AttemptsUsed != count {
		out.Violations = append(out.Violations, fmt.Sprintf("a 429 used an attempt: %d attempts for %d messages", email.AttemptsUsed, count))
	}
	record(t, out)
}

func TestT05ProviderQuota(t *testing.T) {
	count := scaled(1_500)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(5)
	r.provider.quota = 50
	r.startWorkers(1)
	status := r.waitUntilDone()
	out := r.finish("T05 provider quota 50/s", "How well does a send fit a provider that really allows only 50 a second?",
		fmt.Sprintf("One worker, %d emails, calls past 50 in any second answer 429 with Retry-After: 1.", count), status)
	email := out.Channels["email"]
	out.Notes = append(out.Notes, fmt.Sprintf("%d calls for %d messages, %d of them 429. Accepted %.1f/s. First to last call %.1f s (%.1f s is the quota's own pace).",
		email.Requests, count, email.Answers[answer429], email.AcceptedRate, email.SpanSeconds, float64(count)/50))
	if email.AttemptsUsed != count {
		out.Violations = append(out.Violations, fmt.Sprintf("a 429 used an attempt: %d attempts for %d messages", email.AttemptsUsed, count))
	}
	record(t, out)
}

func TestT08OneChannelRateLimited(t *testing.T) {
	sms, email := scaled(2_000), scaled(500)
	r := newRun(t, map[string]int{"sms": sms, "email": email})
	r.provider.latency = usualLatency(8)
	const limited = 15 * time.Second
	var began time.Time
	var once sync.Once
	r.provider.decide = func(channel string, _ int) string {
		once.Do(func() { began = time.Now() })
		if channel == "email" && time.Since(began) < limited {
			return answer429
		}
		return answerAccept
	}
	r.startWorkers(2)
	status := r.waitUntilDone()
	out := r.finish("T08 email limited, sms not", "Does a limited channel leave the other one alone?",
		fmt.Sprintf("Two workers, %d sms and %d emails, every email answers 429 for the first %s.", sms, email, limited), status)
	text, mail := out.Channels["sms"], out.Channels["email"]
	out.Notes = append(out.Notes, fmt.Sprintf("Sms sent in that time: %d. Its longest gap: %.2f s. Emails tried: %d calls, %d answered 429.",
		r.provider.sentBetween("sms", began, began.Add(limited)), text.LongestGap, mail.Requests, mail.Answers[answer429]))
	if mail.AttemptsUsed != email {
		out.Violations = append(out.Violations, fmt.Sprintf("a 429 used an attempt: %d attempts for %d messages", mail.AttemptsUsed, email))
	}
	record(t, out)
}

// TestT11RedisGoesDown takes the limiter away mid-send. A call that left later than a
// moment after that had no permission to go.
func TestT11RedisGoesDown(t *testing.T) {
	count := scaled(1_500)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(11)
	r.startWorkers(2)
	for r.provider.callCount("email") < count/3 {
		time.Sleep(20 * time.Millisecond)
	}
	compose(t, "kill", "redis")
	down := time.Now()
	const outage = 10 * time.Second
	time.Sleep(outage)
	back := time.Now()
	compose(t, "start", "redis")
	status := r.waitUntilDone()
	after := r.provider.sentBetween("email", down.Add(time.Second), back)
	out := r.finish("T11 redis goes down", "Does sending stop without the limiter, and restart without a burst?",
		fmt.Sprintf("Two workers, %d emails, Redis killed after %d calls and started again %s later.", count, count/3, outage), status)
	email := out.Channels["email"]
	out.Notes = append(out.Notes, fmt.Sprintf("Calls that left in the first second of the outage: %d. Later in it: %d. Longest gap %.2f s; in the 100 ms after it %d calls, in the second after it %d.",
		r.provider.sentBetween("email", down, down.Add(time.Second)), after, email.LongestGap, email.AfterGap100ms, email.AfterGapSecond))
	if after > 0 {
		out.Violations = append(out.Violations, fmt.Sprintf("%d calls left with Redis down for over a second", after))
	}
	record(t, out)
}

// TestT12PostgresGoesAway cuts every connection and refuses new ones. The container
// keeps running, because its data lives on tmpfs and would not survive a stop.
func TestT12PostgresGoesAway(t *testing.T) {
	count := scaled(1_500)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(12)
	r.startWorkers(2)
	for r.provider.callCount("email") < count/3 {
		time.Sleep(20 * time.Millisecond)
	}
	name := r.databaseName()
	r.adminExec(`ALTER DATABASE ` + name + ` WITH ALLOW_CONNECTIONS false`)
	r.adminExec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '` + name + `'`)
	down := time.Now()
	const outage = 10 * time.Second
	time.Sleep(outage)
	back := time.Now()
	r.adminExec(`ALTER DATABASE ` + name + ` WITH ALLOW_CONNECTIONS true`)
	status := r.waitUntilDone()
	after := r.provider.sentBetween("email", down.Add(time.Second), back)
	out := r.finish("T12 postgres goes away", "Are messages and their state still right after losing the database mid-send?",
		fmt.Sprintf("Two workers, %d emails, every database connection cut after %d calls and refused for %s.", count, count/3, outage), status)
	out.Notes = append(out.Notes, fmt.Sprintf("Calls that left over a second into the outage: %d. Finished %.0f s after it ended. The lease is 60 s. Repeats recognised by key: %d. Abandoned: %d.",
		after, time.Since(back).Seconds(), out.KeyRepeats, out.Abandoned))
	record(t, out)
}

// TestT13WorkersRestart kills every worker and starts new ones at once. The new workers
// must not make up for lost time, and what the old ones held comes back after its lease.
func TestT13WorkersRestart(t *testing.T) {
	count := scaled(1_500)
	r := newRun(t, map[string]int{"email": count})
	r.provider.latency = usualLatency(13)
	r.startWorkers(2)
	for r.provider.callCount("email") < count/3 {
		time.Sleep(20 * time.Millisecond)
	}
	r.kill(0)
	r.kill(1)
	r.startWorkers(2)
	r.workers = r.workers[2:]
	status := r.waitUntilDone()
	out := r.finish("T13 every worker restarts", "Is there a burst after a restart, or when held messages come back?",
		fmt.Sprintf("Two workers, %d emails, both get SIGKILL after %d calls and two new ones start at once.", count, count/3), status)
	email := out.Channels["email"]
	out.Notes = append(out.Notes, fmt.Sprintf("Longest gap %.2f s; in the 100 ms after it %d calls, in the second after it %d. Repeats recognised by key: %d.",
		email.LongestGap, email.AfterGap100ms, email.AfterGapSecond, out.KeyRepeats))
	record(t, out)
}

// TestT14LimitFollowsTheSetting checks that a lower limit really lowers the rate. The
// run fails on its own if the provider sees more than the limit in any second.
func TestT14LimitFollowsTheSetting(t *testing.T) {
	for _, chosen := range []int{50, 5} {
		t.Run(fmt.Sprintf("limit%d", chosen), func(t *testing.T) {
			count := max(scaled(chosen*15), 20)
			r := newRun(t, map[string]int{"email": count})
			r.limit = chosen
			r.provider.latency = usualLatency(14)
			r.startWorkers(2)
			status := r.waitUntilDone()
			out := r.finish(fmt.Sprintf("T14 limit %d", chosen), "Does the rate follow RATE_LIMIT_PER_SECOND?",
				fmt.Sprintf("Two workers, %d emails, a limit of %d a second.", count, chosen), status)
			out.Notes = append(out.Notes, fmt.Sprintf("Accepted %.1f/s against a limit of %d.", out.Channels["email"].AcceptedRate, chosen))
			record(t, out)
		})
	}
}

// TestT14bBunchingUnderALowerLimit repeats the T06b case with half the limit and half
// the group, to see if the margin holds when the rate is lower.
func TestT14bBunchingUnderALowerLimit(t *testing.T) {
	const chosen, delay = 50, 200 * time.Millisecond
	count := scaled(1_000)
	r := newRun(t, map[string]int{"email": count})
	r.limit = chosen
	r.provider.latency = usualLatency(6)
	var (
		mu    sync.Mutex
		calls int
	)
	r.provider.network = func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if (calls-1)/chosen%2 == 0 {
			return delay
		}
		return 0
	}
	r.startWorkers(4)
	status := r.waitUntilDone()
	record(t, r.finish("T14b bunching under a limit of 50", "Does the margin hold at a lower limit?",
		fmt.Sprintf("Four workers, %d emails, a limit of %d, every other %d calls delayed %s.", count, chosen, chosen, delay), status))
}
