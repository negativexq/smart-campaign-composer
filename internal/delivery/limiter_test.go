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
		grants []time.Time
		wg     sync.WaitGroup
	)
	for range 3 { // three workers sharing one limit
		limiter := delivery.NewRedisLimiter(client, limit, prefix)
		wg.Go(func() {
			for got := 0; got < limit; {
				permits, err := limiter.Reserve(context.Background(), "sms", 3)
				if err != nil {
					t.Errorf("reserve: %v", err)
					return
				}
				now := time.Now()
				mu.Lock()
				for range permits.Granted {
					grants = append(grants, now)
				}
				mu.Unlock()
				got += permits.Granted
				time.Sleep(permits.Retry)
			}
		})
	}
	wg.Wait()

	slices.SortFunc(grants, func(a, b time.Time) int { return a.Compare(b) })
	// Times are taken after the grant returns, so this leaves room for scheduling
	// noise, and the margin above a full second is what keeps the limit at the provider.
	for i := limit; i < len(grants); i++ {
		if span := grants[i].Sub(grants[i-limit]); span < time.Second {
			t.Fatalf("%d sends in %v", limit+1, span)
		}
	}
}

func TestLimiterGrantsOnlyWhatFits(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 5, testsupport.Unique(t))
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
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 5, testsupport.Unique(t))
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", 5)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed, err := limiter.Settle(ctx, "sms", reserved, 3); err != nil || confirmed != 3 {
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
	stalled := delivery.NewRedisLimiter(client, limit, prefix)
	other := delivery.NewRedisLimiter(client, limit, prefix)
	ctx := context.Background()

	reserved, err := stalled.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if permits, err := other.Reserve(ctx, "sms", limit); err != nil || permits.Granted != limit {
		t.Fatalf("other worker: %+v, %v", permits, err)
	}
	if _, err := stalled.Settle(ctx, "sms", reserved, 0); err != nil {
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
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), limit, testsupport.Unique(t))
	ctx := context.Background()
	reserved, err := limiter.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if _, err := limiter.Settle(ctx, "sms", reserved, limit); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if permits, err := limiter.Reserve(ctx, "sms", limit); err != nil || permits.Granted != 0 {
		t.Fatalf("sends 500 ms old were not counted: %+v, %v", permits, err)
	}
}

// A permit that expired during a claim is only good if the window still has room.
func TestAnExpiredPermitIsNotConfirmedOverTheLimit(t *testing.T) {
	t.Parallel()
	const limit = 10
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	stalled := delivery.NewRedisLimiter(client, limit, prefix)
	other := delivery.NewRedisLimiter(client, limit, prefix)
	ctx := context.Background()

	reserved, err := stalled.Reserve(ctx, "sms", limit)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := other.Reserve(ctx, "sms", 7); err != nil {
		t.Fatal(err)
	}
	if confirmed, err := stalled.Settle(ctx, "sms", reserved, limit); err != nil || confirmed != 3 {
		t.Fatalf("confirmed: %d, %v", confirmed, err)
	}
}

// Workers overlap and some claims stall, yet what the provider receives holds to the
// limit in every second. Times are taken just before the simulated call.
func TestProviderNeverSeesMoreThanTheLimitAcrossWorkers(t *testing.T) {
	t.Parallel()
	const (
		limit    = 100
		duration = 4 * time.Second
	)
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	var (
		mu    sync.Mutex
		sends []time.Time
		wg    sync.WaitGroup
	)
	deadline := time.Now().Add(duration)
	for worker := range 4 {
		limiter := delivery.NewRedisLimiter(client, limit, prefix)
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
				confirmed, err := limiter.Settle(context.Background(), "sms", reserved, claimed)
				if err != nil {
					t.Errorf("settle: %v", err)
					return
				}
				now := time.Now()
				mu.Lock()
				for range confirmed {
					sends = append(sends, now)
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	slices.SortFunc(sends, func(a, b time.Time) int { return a.Compare(b) })
	if len(sends) < limit {
		t.Fatalf("only %d sends, the test proved nothing", len(sends))
	}
	for i := limit; i < len(sends); i++ {
		if span := sends[i].Sub(sends[i-limit]); span < time.Second {
			t.Fatalf("%d sends in %v", limit+1, span)
		}
	}
}

func TestLimiterKeepsChannelsApart(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 1, testsupport.Unique(t))
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
	if _, err := delivery.NewRedisLimiter(client, 10, "x").Reserve(context.Background(), "sms", 1); err == nil {
		t.Fatal("no limiter must mean no permission to send")
	}
}

func TestPauseHoldsEveryWorkerOnThatChannelOnly(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	first, second := delivery.NewRedisLimiter(client, 100, prefix), delivery.NewRedisLimiter(client, 100, prefix)
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
	limiter := delivery.NewRedisLimiter(client, 100, prefix)
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
	if err := delivery.NewRedisLimiter(client, 10, "x").Pause(context.Background(), "sms", time.Second); err == nil {
		t.Fatal("expected an error")
	}
}
