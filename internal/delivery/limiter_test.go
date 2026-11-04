package delivery_test

import (
	"context"
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

func TestReleasedPermitsCanBeGrantedAgain(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 5, testsupport.Unique(t))
	ctx := context.Background()
	if _, err := limiter.Reserve(ctx, "sms", 5); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Release(ctx, "sms", 2); err != nil {
		t.Fatal(err)
	}
	permits, err := limiter.Reserve(ctx, "sms", 5)
	if err != nil || permits.Granted != 2 {
		t.Fatalf("permits: %+v, %v", permits, err)
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
