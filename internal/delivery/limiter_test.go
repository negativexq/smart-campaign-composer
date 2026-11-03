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
