package delivery_test

import (
	"context"
	"errors"
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
		for range limit {
			wg.Go(func() {
				if _, err := limiter.Wait(context.Background(), "sms"); err != nil {
					t.Errorf("wait: %v", err)
					return
				}
				mu.Lock()
				grants = append(grants, time.Now())
				mu.Unlock()
			})
		}
	}
	wg.Wait()

	slices.SortFunc(grants, func(a, b time.Time) int { return a.Compare(b) })
	// Times are taken after the grant returns, so allow for some scheduling noise.
	for i := limit; i < len(grants); i++ {
		if span := grants[i].Sub(grants[i-limit]); span < 900*time.Millisecond {
			t.Fatalf("%d sends in %v", limit+1, span)
		}
	}
}

func TestLimiterKeepsChannelsApart(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 1, testsupport.Unique(t))
	start := time.Now()
	for _, channel := range []string{"sms", "email"} {
		if _, err := limiter.Wait(context.Background(), channel); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("email waited for SMS: %v", elapsed)
	}
}

func TestLimiterStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	limiter := delivery.NewRedisLimiter(testsupport.NewRedis(t), 1, testsupport.Unique(t))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := limiter.Wait(ctx, "sms"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := limiter.Wait(ctx, "sms"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestLimiterFailsWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	if _, err := delivery.NewRedisLimiter(client, 10, "x").Wait(context.Background(), "sms"); err == nil {
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
	waited, err := second.Wait(ctx, "sms")
	if err != nil {
		t.Fatal(err)
	}
	if waited < 200*time.Millisecond {
		t.Fatalf("another worker waited only %v", waited)
	}
	start := time.Now()
	if err := first.Pause(ctx, "sms", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Wait(ctx, "email"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("email was held for %v", elapsed)
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
	if waited, err := limiter.Wait(ctx, "sms"); err != nil || waited < 300*time.Millisecond {
		t.Fatalf("waited %v, err %v", waited, err)
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
