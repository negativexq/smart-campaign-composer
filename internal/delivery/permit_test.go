package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

func newPermit(t *testing.T, interval time.Duration, burst int) *delivery.RedisPermit {
	t.Helper()
	return delivery.NewRedisPermit(testsupport.NewRedis(t), interval, burst, testsupport.Unique(t))
}

func TestAPermitIsOneSendHoweverManyAreWanted(t *testing.T) {
	t.Parallel()
	first, err := newPermit(t, 50*time.Millisecond, 1).Reserve(context.Background(), "sms", 32)
	if err != nil || first.Granted != 1 {
		t.Fatalf("first: %+v, %v", first, err)
	}
}

// TestACommandRunTwiceCostsATurnAndCreatesNone is what a retry after a lost reply does:
// the first run took a turn that nobody got to use.
func TestACommandRunTwiceCostsATurnAndCreatesNone(t *testing.T) {
	t.Parallel()
	const interval = 200 * time.Millisecond
	permit := newPermit(t, interval, 1)
	ctx := context.Background()
	lost, err := permit.Reserve(ctx, "sms", 1)
	if err != nil || lost.Granted != 1 {
		t.Fatalf("lost: %+v, %v", lost, err)
	}
	again, err := permit.Reserve(ctx, "sms", 1)
	if err != nil || again.Granted != 0 {
		t.Fatalf("a repeat must not get a turn of its own: %+v, %v", again, err)
	}
	if again.Retry < interval/2 || again.Retry > interval {
		t.Fatalf("wait %v, the next turn is one interval on", again.Retry)
	}
}

func TestAnUnusedPermitIsNotGivenBack(t *testing.T) {
	t.Parallel()
	permit := newPermit(t, 200*time.Millisecond, 1)
	ctx := context.Background()
	first, err := permit.Reserve(ctx, "sms", 1)
	if err != nil || first.Granted != 1 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	for range 3 {
		if confirmed, err := permit.Settle(ctx, "sms", first, 0); err != nil || confirmed != 0 {
			t.Fatalf("settle: %d, %v", confirmed, err)
		}
	}
	if next, err := permit.Reserve(ctx, "sms", 1); err != nil || next.Granted != 0 {
		t.Fatalf("settling created a turn: %+v, %v", next, err)
	}
}

func TestAnOldPermitIsRefusedWithoutRedis(t *testing.T) {
	t.Parallel()
	options := testsupport.NewRedis(t).Options()
	client := redis.NewClient(options)
	permit := delivery.NewRedisPermit(client, 10*time.Millisecond, 1, testsupport.Unique(t))
	ctx := context.Background()
	taken, err := permit.Reserve(ctx, "sms", 1)
	if err != nil || taken.Granted != 1 {
		t.Fatalf("reserve: %+v, %v", taken, err)
	}
	if confirmed, err := permit.Settle(ctx, "sms", taken, 1); err != nil || confirmed != 1 {
		t.Fatalf("fresh: %d, %v", confirmed, err)
	}
	client.Close()
	time.Sleep(250 * time.Millisecond)
	if confirmed, err := permit.Settle(ctx, "sms", taken, 1); err != nil || confirmed != 0 {
		t.Fatalf("old: %d, %v", confirmed, err)
	}
}

func TestPermitsFailWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	if _, err := delivery.NewRedisPermit(client, 12*time.Millisecond, 1, "x").Reserve(context.Background(), "sms", 1); err == nil {
		t.Fatal("no limiter must mean no permission to send")
	}
}

func TestAPauseHoldsPermitsOnThatChannelOnlyWithNoCatchUp(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	// Long enough that the two calls after the pause fall within one turn on a busy machine.
	const interval = 500 * time.Millisecond
	first, second := delivery.NewRedisPermit(client, interval, 1, prefix), delivery.NewRedisPermit(client, interval, 1, prefix)
	ctx := context.Background()
	if err := first.Pause(ctx, "sms", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	held, err := second.Reserve(ctx, "sms", 1)
	if err != nil || held.Granted != 0 || !held.Paused || held.Retry < 200*time.Millisecond {
		t.Fatalf("another worker: %+v, %v", held, err)
	}
	if other, err := second.Reserve(ctx, "email", 1); err != nil || other.Granted != 1 {
		t.Fatalf("email: %+v, %v", other, err)
	}
	time.Sleep(350 * time.Millisecond)
	if after, err := second.Reserve(ctx, "sms", 1); err != nil || after.Granted != 1 {
		t.Fatalf("after the pause: %+v, %v", after, err)
	}
	if next, err := first.Reserve(ctx, "sms", 1); err != nil || next.Granted != 0 {
		t.Fatalf("a pause must not save up turns: %+v, %v", next, err)
	}
}

func TestPermitsHoldThePaceAcrossWorkers(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	ctx := context.Background()
	const (
		interval = 10 * time.Millisecond
		duration = 2 * time.Second
	)
	began := time.Now()
	counts := make(chan int, 4)
	for range 4 {
		permit := delivery.NewRedisPermit(client, interval, 1, prefix)
		go func() {
			total := 0
			for time.Since(began) < duration {
				taken, err := permit.Reserve(ctx, "sms", 1)
				if err != nil {
					t.Errorf("reserve: %v", err)
					break
				}
				if taken.Granted == 0 {
					time.Sleep(taken.Retry)
					continue
				}
				total++
			}
			counts <- total
		}()
	}
	total := 0
	for range 4 {
		total += <-counts
	}
	if most := int(time.Since(began)/interval) + 1; total > most {
		t.Fatalf("%d permits, the pace allows %d", total, most)
	}
	if total < 100 {
		t.Fatalf("only %d permits at one per 10 ms", total)
	}
}

func TestThePaceStaysWithinTheLimit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		limit    int
		interval time.Duration
		burst    int
	}{
		{100, 12500 * time.Microsecond, 2},
		{50, 25 * time.Millisecond, 2},
		{10, 125 * time.Millisecond, 2},
		{9, time.Second * 10 / 72, 1},
		{1, 1250 * time.Millisecond, 1},
	} {
		if interval := delivery.PaceInterval(test.limit); interval != test.interval {
			t.Errorf("limit %d: interval %v, want %v", test.limit, interval, test.interval)
		}
		if burst := delivery.PermitBurst(test.limit); burst != test.burst {
			t.Errorf("limit %d: burst %d, want %d", test.limit, burst, test.burst)
		}
	}
	// However the turns fall, no second may hold more than the limit.
	for limit := 1; limit <= 200; limit++ {
		turnsInASecond := int(time.Second/delivery.PaceInterval(limit)) + delivery.PermitBurst(limit)
		if turnsInASecond > limit {
			t.Errorf("limit %d allows %d turns in a second", limit, turnsInASecond)
		}
	}
}

func TestASettingOfFiftyGivesFewerPermitsThanOneOfAHundred(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	ctx := context.Background()
	count := func(limit int) int {
		permit := delivery.NewPermitForLimit(client, limit, testsupport.Unique(t))
		total := 0
		for ended := time.Now().Add(time.Second); time.Now().Before(ended); {
			taken, err := permit.Reserve(ctx, "sms", 1)
			if err != nil {
				t.Fatal(err)
			}
			total += taken.Granted
			time.Sleep(max(taken.Retry, 200*time.Microsecond))
		}
		return total
	}
	if fifty := count(50); fifty > 42 {
		t.Fatalf("a limit of 50 gave %d permits in a second", fifty)
	}
	if hundred := count(100); hundred > 82 {
		t.Fatalf("a limit of 100 gave %d permits in a second", hundred)
	}
}
