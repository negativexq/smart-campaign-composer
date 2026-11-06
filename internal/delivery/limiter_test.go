package delivery_test

import (
	"context"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

func TestLimiterHoldsTheRateAcrossWorkers(t *testing.T) {
	t.Parallel()
	const limit = 20
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)

	var (
		mu     sync.Mutex
		grants []grant
		wg     sync.WaitGroup
	)
	for range 3 { // three workers sharing one limit
		limiter := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
		wg.Go(func() {
			for got := 0; got < limit; {
				before := time.Now()
				permits, err := limiter.Reserve(context.Background(), "sms", 3)
				if err != nil {
					t.Errorf("reserve: %v", err)
					return
				}
				after := time.Now()
				mu.Lock()
				for range permits.Granted {
					grants = append(grants, grant{before, after})
				}
				mu.Unlock()
				got += permits.Granted
				time.Sleep(permits.Retry)
			}
		})
	}
	wg.Wait()

	slices.SortFunc(grants, func(a, b grant) int { return a.after.Compare(b.after) })
	// Each grant happened between the two times taken around it, so a span measured
	// from the earlier one's start to the later one's end can only be longer.
	for i := limit; i < len(grants); i++ {
		if span := grants[i].after.Sub(grants[i-limit].before); span < time.Second {
			t.Fatalf("%d sends in %v", limit+1, span)
		}
	}
}

type grant struct{ before, after time.Time }

// hold is how long a call may stay in flight in these tests.
const hold = 2 * time.Second

func TestLimiterGrantsOnlyWhatFits(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 5, testsupport.Unique(t)).RecoverAfter(0)
	ctx := context.Background()
	first, err := limiter.Reserve(ctx, "sms", 3)
	if err != nil || first.Granted != 3 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	second, err := limiter.Reserve(ctx, "sms", 3)
	if err != nil || second.Granted != 2 {
		t.Fatalf("second: %+v, %v", second, err)
	}
	third, err := limiter.Reserve(ctx, "sms", 3)
	if err != nil || third.Granted != 0 || third.Retry <= 0 || third.Paused {
		t.Fatalf("third: %+v, %v", third, err)
	}
}

func TestUnusedPermitsCanBeGrantedAgain(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 5, testsupport.Unique(t)).RecoverAfter(0)
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", 5)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed, err := limiter.Settle(ctx, "sms", reserved, 3, hold); err != nil || confirmed != 3 {
		t.Fatalf("confirmed: %d, %v", confirmed, err)
	}
	permits, err := limiter.Reserve(ctx, "sms", 5)
	if err != nil || permits.Granted != 2 {
		t.Fatalf("permits: %+v, %v", permits, err)
	}
}

// A worker whose claim stalled past the window has nothing left to give back. Taking
// the newest permits instead would erase sends another worker really made.
func TestSettlingCannotEraseAnotherWorkersPermits(t *testing.T) {
	t.Parallel()
	const limit = 10
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	stalled := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
	other := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
	ctx := context.Background()

	reserved, err := stalled.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if permits, err := other.Reserve(ctx, "sms", limit); err != nil || permits.Granted != limit {
		t.Fatalf("other worker: %+v, %v", permits, err)
	}
	if _, err := stalled.Settle(ctx, "sms", reserved, 0, hold); err != nil {
		t.Fatal(err)
	}
	if permits, err := other.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("the other worker's sends were forgotten: %+v, %v", permits, err)
	}
}

// A send counts from when it goes out, not from when its permit was granted, or a
// slow claim lets it land in the same second as permits granted after it.
func TestPermitsCountFromWhenTheyAreUsed(t *testing.T) {
	t.Parallel()
	const limit = 10
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), limit, testsupport.Unique(t)).RecoverAfter(0)
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if _, err := limiter.Settle(ctx, "sms", reserved, limit, hold); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("sends 500 ms old were not counted: %+v, %v", permits, err)
	}
}

// Redis can lose the window while calls are out, by a restart, a flush or an
// eviction. The channel then grants nothing until every call that could have been
// out has stopped counting, so the provider's second is not given out twice. A first
// start cannot tell the difference, so it waits too.
func TestAChannelWaitsAfterRedisLostItsWindow(t *testing.T) {
	t.Parallel()
	const limit = 10
	const wait = 400 * time.Millisecond
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	limiter := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(wait)
	ctx := context.Background()

	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 || permits.Retry <= 0 || permits.Retry > wait || permits.Paused {
		t.Fatalf("first start: %+v, %v", permits, err)
	}
	time.Sleep(wait + 50*time.Millisecond)
	reserved, err := limiter.Reserve(ctx, "sms", limit)
	if err != nil || reserved.Granted != limit {
		t.Fatalf("after the wait: %+v, %v", reserved, err)
	}
	if _, err := limiter.Settle(ctx, "sms", reserved, limit, hold); err != nil {
		t.Fatal(err)
	}

	keys, err := client.Keys(ctx, prefix+"*").Result()
	if err != nil || len(keys) == 0 {
		t.Fatalf("keys: %v, %v", keys, err)
	}
	if err := client.Del(ctx, keys...).Err(); err != nil {
		t.Fatal(err)
	}
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("the lost second was given out again: %+v, %v", permits, err)
	}
	if permits, err := limiter.Reserve(ctx, "email", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("every channel lost its window: %+v, %v", permits, err)
	}
	time.Sleep(wait + 50*time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != limit {
		t.Fatalf("after the wait: %+v, %v", permits, err)
	}
}

// A permit that expired during a claim is only good if the window still has room.
func TestAnExpiredPermitIsNotConfirmedOverTheLimit(t *testing.T) {
	t.Parallel()
	const limit = 10
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	stalled := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
	other := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
	ctx := context.Background()

	reserved, err := stalled.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := other.Reserve(ctx, "sms", 7); err != nil {
		t.Fatal(err)
	}
	if confirmed, err := stalled.Settle(ctx, "sms", reserved, limit, hold); err != nil || confirmed != 3 {
		t.Fatalf("confirmed: %d, %v", confirmed, err)
	}
}

// Workers overlap, some claims stall, and some batches are held up on the way to the
// provider while the next is not. What the provider receives still holds to the
// limit in every second, because a call counts until its answer comes back and for a
// second after it. A call that never gets an answer counts for its whole hold.
func TestProviderNeverSeesMoreThanTheLimitAcrossWorkers(t *testing.T) {
	t.Parallel()
	const (
		limit    = 100
		duration = 5 * time.Second
	)
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	var (
		mu       sync.Mutex
		arrivals []time.Time
		wg       sync.WaitGroup
		calls    sync.WaitGroup
	)
	deadline := time.Now().Add(duration)
	for worker := range 4 {
		limiter := delivery.NewRedisLimiter(client, limit, prefix).RecoverAfter(0)
		random := rand.New(rand.NewPCG(uint64(worker), 42)) //nolint:gosec // test timing
		wg.Go(func() {
			for time.Now().Before(deadline) {
				reserved, err := limiter.Reserve(context.Background(), "sms", 1+random.IntN(32))
				if err != nil {
					t.Errorf("reserve: %v", err)
					return
				}
				if reserved.Granted == 0 {
					time.Sleep(min(reserved.Retry, 50*time.Millisecond))
					continue
				}
				claim := time.Duration(random.IntN(5)) * time.Millisecond
				switch random.IntN(20) {
				case 0:
					claim = 150*time.Millisecond + time.Duration(random.IntN(250))*time.Millisecond
				case 1:
					claim = 1200 * time.Millisecond
				}
				time.Sleep(claim)
				claimed := random.IntN(reserved.Granted + 1)
				confirmed, err := limiter.Settle(context.Background(), "sms", reserved, claimed, hold)
				if err != nil {
					t.Errorf("settle: %v", err)
					return
				}
				// One batch in eight is held up on the way, as when the machine stalls.
				way := time.Duration(random.IntN(5)) * time.Millisecond
				if random.IntN(8) == 0 {
					way = 150*time.Millisecond + time.Duration(random.IntN(250))*time.Millisecond
				}
				for range confirmed {
					answer := time.Duration(random.IntN(30)) * time.Millisecond
					lost := random.IntN(50) == 0
					calls.Go(func() {
						time.Sleep(way)
						mu.Lock()
						arrivals = append(arrivals, time.Now())
						mu.Unlock()
						time.Sleep(answer)
						if !lost {
							limiter.Answered("sms", reserved, time.Now())
						}
					})
				}
			}
		})
	}
	wg.Wait()
	calls.Wait()

	slices.SortFunc(arrivals, func(a, b time.Time) int { return a.Compare(b) })
	if len(arrivals) < 3*limit {
		t.Fatalf("only %d calls in %v, the limit held the channel back", len(arrivals), duration)
	}
	for i := limit; i < len(arrivals); i++ {
		if span := arrivals[i].Sub(arrivals[i-limit]); span < time.Second {
			t.Fatalf("%d calls reached the provider in %v", limit+1, span)
		}
	}
}

// A call counts while it is in flight, so its answer is what frees the window, and
// it counts for 1.1 s from that answer.
func TestACallCountsUntilTheWindowAfterItsAnswer(t *testing.T) {
	t.Parallel()
	const limit = 5
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), limit, testsupport.Unique(t)).RecoverAfter(0)
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Settle(ctx, "sms", reserved, limit, hold); err != nil {
		t.Fatal(err)
	}
	// In flight for longer than the window: still counted.
	time.Sleep(1200 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("calls in flight were not counted: %+v, %v", permits, err)
	}
	for range limit {
		limiter.Answered("sms", reserved, time.Now())
	}
	// The answers ride on this trip: the calls now count from them.
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("answered calls stopped counting at once: %+v, %v", permits, err)
	}
	// The window is 1.1 s, so a second after the answers they still count.
	time.Sleep(1000 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("a second after the answers, before the 100 ms margin: %+v, %v", permits, err)
	}
	time.Sleep(200 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != limit {
		t.Fatalf("1.2 s after the answers: %+v, %v", permits, err)
	}
}

// A call with no answer may still reach the provider, so it counts for its whole hold.
func TestACallWithNoAnswerCountsForItsHold(t *testing.T) {
	t.Parallel()
	const limit = 3
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), limit, testsupport.Unique(t)).RecoverAfter(0)
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Settle(ctx, "sms", reserved, limit, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("within a second after the hold: %+v, %v", permits, err)
	}
	time.Sleep(500 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != limit {
		t.Fatalf("after the hold and a second: %+v, %v", permits, err)
	}
}

func TestLimiterKeepsChannelsApart(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 1, testsupport.Unique(t)).RecoverAfter(0)
	for _, channel := range []string{"sms", "email"} {
		permits, err := limiter.Reserve(context.Background(), channel, 1)
		if err != nil || permits.Granted != 1 {
			t.Fatalf("%s: %+v, %v", channel, permits, err)
		}
	}
}

func TestLimiterFailsWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	if _, err := delivery.NewRedisLimiter(client, 10, "x").RecoverAfter(0).Reserve(context.Background(), "sms", 1); err == nil {
		t.Fatal("no limiter must mean no permission to send")
	}
}

func TestPauseHoldsEveryWorkerOnThatChannelOnly(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	first, second := delivery.NewRedisLimiter(client, 100, prefix).RecoverAfter(0), delivery.NewRedisLimiter(client, 100, prefix).RecoverAfter(0)
	ctx := context.Background()

	if err := first.Pause(ctx, "sms", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	permits, err := second.Reserve(ctx, "sms", 1)
	if err != nil {
		t.Fatal(err)
	}
	if permits.Granted != 0 || !permits.Paused || permits.Retry < 200*time.Millisecond {
		t.Fatalf("another worker got %+v", permits)
	}
	if permits, err := second.Reserve(ctx, "email", 1); err != nil || permits.Granted != 1 {
		t.Fatalf("email: %+v, %v", permits, err)
	}
}

func TestAPauseIsNotShortenedAndHasACeiling(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	limiter := delivery.NewRedisLimiter(client, 100, prefix).RecoverAfter(0)
	ctx := context.Background()

	if err := limiter.Pause(ctx, "sms", 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Pause(ctx, "sms", time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if permits, err := limiter.Reserve(ctx, "sms", 1); err != nil || permits.Retry < 300*time.Millisecond {
		t.Fatalf("permits %+v, err %v", permits, err)
	}

	// A provider asking for a day must not stop every campaign for a day.
	if err := limiter.Pause(ctx, "email", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	remaining, err := client.PTTL(ctx, prefix+":pause:email").Result()
	if err != nil {
		t.Fatal(err)
	}
	if remaining <= 0 || remaining > time.Minute {
		t.Fatalf("paused for %v", remaining)
	}
}

func TestPauseFailsWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	if err := delivery.NewRedisLimiter(client, 10, "x").RecoverAfter(0).Pause(context.Background(), "sms", time.Second); err == nil {
		t.Fatal("expected an error")
	}
}

// While credentials are refused, a channel gets one call every probe interval, across
// every worker, and refusals that keep coming neither move that probe back nor start
// a new lock.
func TestALockedChannelGrantsOneProbeEachIntervalAcrossWorkers(t *testing.T) {
	t.Parallel()
	const every = 300 * time.Millisecond
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	ctx := context.Background()
	first := delivery.NewRedisLimiter(client, 100, prefix).RecoverAfter(0).ProbeEvery(every)
	if locked, err := first.LockCredentials(ctx, "sms"); err != nil || !locked {
		t.Fatalf("lock: %v, %v", locked, err)
	}
	locked := time.Now()

	var (
		mu     sync.Mutex
		probes []time.Time
		gens   = map[int64]bool{}
		other  int
		wg     sync.WaitGroup
	)
	for range 4 { // four workers asking as fast as they can
		limiter := delivery.NewRedisLimiter(client, 100, prefix).RecoverAfter(0).ProbeEvery(every)
		wg.Go(func() {
			for time.Since(locked) < 1100*time.Millisecond {
				if again, err := limiter.LockCredentials(ctx, "sms"); err != nil || again {
					t.Errorf("a refusal while locked started a new lock: %v, %v", again, err)
				}
				permits, err := limiter.Reserve(ctx, "sms", 10)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				switch {
				case permits.Probe != 0:
					probes, gens[permits.Probe] = append(probes, time.Now()), true
					if permits.Granted != 1 {
						t.Errorf("a probe was granted %d calls", permits.Granted)
					}
				case permits.Granted > 0:
					other += permits.Granted
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	wg.Wait()
	if other != 0 {
		t.Fatalf("%d calls were granted besides probes", other)
	}
	if len(probes) != 3 || len(gens) != 1 {
		t.Fatalf("%d probes over %d generations in 1.1 s at one every %v", len(probes), len(gens), every)
	}
	slices.SortFunc(probes, func(a, b time.Time) int { return a.Compare(b) })
	if probes[0].Sub(locked) < every-20*time.Millisecond {
		t.Fatalf("first probe %v after the lock", probes[0].Sub(locked))
	}
	for i := 1; i < len(probes); i++ {
		if gap := probes[i].Sub(probes[i-1]); gap < every-20*time.Millisecond {
			t.Fatalf("probes %v apart", gap)
		}
	}
}

// A probe opens only the lock it tested: a late answer from before a newer lock
// leaves that one in place.
func TestAProbeOpensOnlyTheLockItTested(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 100, testsupport.Unique(t)).RecoverAfter(0).ProbeEvery(50 * time.Millisecond)
	ctx := context.Background()
	probe := func() int64 {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			permits, err := limiter.Reserve(ctx, "sms", 5)
			if err != nil {
				t.Fatal(err)
			}
			if permits.Probe != 0 {
				return permits.Probe
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("no probe was granted")
		return 0
	}
	if _, err := limiter.LockCredentials(ctx, "sms"); err != nil {
		t.Fatal(err)
	}
	stale := probe()
	if opened, err := limiter.UnlockCredentials(ctx, "sms", stale); err != nil || !opened {
		t.Fatalf("the probe of the lock in place did not open it: %v, %v", opened, err)
	}
	if locked, err := limiter.LockCredentials(ctx, "sms"); err != nil || !locked {
		t.Fatalf("a refusal after opening did not lock again: %v, %v", locked, err)
	}
	if opened, err := limiter.UnlockCredentials(ctx, "sms", stale); err != nil || opened {
		t.Fatalf("a late probe opened a newer lock: %v, %v", opened, err)
	}
	if locked, err := limiter.CredentialsLocked(ctx, "sms"); err != nil || !locked {
		t.Fatalf("locked: %v, %v", locked, err)
	}
	if opened, err := limiter.UnlockCredentials(ctx, "sms", probe()); err != nil || !opened {
		t.Fatalf("the newer lock's probe did not open it: %v, %v", opened, err)
	}
	if permits, err := limiter.Reserve(ctx, "sms", 5); err != nil || permits.Granted != 5 || permits.Probe != 0 {
		t.Fatalf("after opening: %+v, %v", permits, err)
	}
}

// A probe that found nothing to send is handed back, so the lock does not wait a
// whole interval for nothing. The other channel is never held.
func TestAProbeThatSentNothingCanGoAgain(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 100, testsupport.Unique(t)).RecoverAfter(0).ProbeEvery(100 * time.Millisecond)
	ctx := context.Background()
	if _, err := limiter.LockCredentials(ctx, "sms"); err != nil {
		t.Fatal(err)
	}
	if permits, err := limiter.Reserve(ctx, "email", 5); err != nil || permits.Granted != 5 {
		t.Fatalf("the other channel was held: %+v, %v", permits, err)
	}
	time.Sleep(110 * time.Millisecond)
	first, err := limiter.Reserve(ctx, "sms", 5)
	if err != nil || first.Probe == 0 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	if again, err := limiter.Reserve(ctx, "sms", 5); err != nil || again.Granted != 0 || !again.Paused {
		t.Fatalf("a second probe went before the interval: %+v, %v", again, err)
	}
	if err := limiter.ReleaseProbe(ctx, "sms", first.Probe); err != nil {
		t.Fatal(err)
	}
	if again, err := limiter.Reserve(ctx, "sms", 5); err != nil || again.Probe != first.Probe || again.Granted != 1 {
		t.Fatalf("a released probe could not go again: %+v, %v", again, err)
	}
}
