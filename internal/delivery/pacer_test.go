package delivery_test

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

func newPacer(t *testing.T, interval time.Duration) *delivery.RedisPacer {
	t.Helper()
	return delivery.NewRedisPacer(testsupport.NewRedis(t), interval, testsupport.Unique(t))
}

func TestAPacerNeverGrantsABurst(t *testing.T) {
	t.Parallel()
	first, err := newPacer(t, 10*time.Millisecond).Reserve(context.Background(), "sms", 100)
	if err != nil || first.Granted < 1 || first.Granted > 4 {
		t.Fatalf("first: %+v, %v", first, err)
	}
}

func TestAPacerHoldsItsPaceAcrossWorkers(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	ctx := context.Background()
	const duration = 2 * time.Second
	counts := make(chan int, 4)
	deadline := time.Now().Add(duration)
	for range 4 {
		pacer := delivery.NewRedisPacer(client, 10*time.Millisecond, prefix)
		go func() {
			total := 0
			for time.Now().Before(deadline) {
				permits, err := pacer.Reserve(ctx, "sms", 32)
				if err != nil {
					t.Errorf("reserve: %v", err)
					break
				}
				if permits.Granted == 0 {
					time.Sleep(permits.Retry)
					continue
				}
				confirmed, err := pacer.Settle(ctx, "sms", permits, permits.Granted)
				if err != nil {
					t.Errorf("settle: %v", err)
					break
				}
				total += confirmed
			}
			counts <- total
		}()
	}
	total := 0
	for range 4 {
		total += <-counts
	}
	if total < 170 || total > 205 {
		t.Fatalf("%d sends in %v at one per 10 ms", total, duration)
	}
}

func TestAPacerTakesBackUnusedTurns(t *testing.T) {
	t.Parallel()
	pacer := newPacer(t, 200*time.Millisecond)
	ctx := context.Background()
	first, err := pacer.Reserve(ctx, "sms", 1)
	if err != nil || first.Granted != 1 {
		t.Fatalf("first: %+v, %v", first, err)
	}
	if early, err := pacer.Reserve(ctx, "sms", 1); err != nil || early.Granted != 0 || early.Retry <= 0 {
		t.Fatalf("before its turn: %+v, %v", early, err)
	}
	if _, err := pacer.Settle(ctx, "sms", first, 0); err != nil {
		t.Fatal(err)
	}
	if again, err := pacer.Reserve(ctx, "sms", 1); err != nil || again.Granted != 1 {
		t.Fatalf("a returned turn was not given out again: %+v, %v", again, err)
	}
}

func TestALateReservationCannotBunchUpWithNewSends(t *testing.T) {
	t.Parallel()
	pacer := newPacer(t, 10*time.Millisecond)
	ctx := context.Background()
	late, err := pacer.Reserve(ctx, "sms", 4)
	if err != nil || late.Granted == 0 {
		t.Fatalf("reserve: %+v, %v", late, err)
	}
	time.Sleep(200 * time.Millisecond)
	began := time.Now()
	fresh, err := pacer.Reserve(ctx, "sms", 4)
	if err != nil || fresh.Granted == 0 {
		t.Fatalf("fresh: %+v, %v", fresh, err)
	}
	if _, err := pacer.Settle(ctx, "sms", fresh, fresh.Granted); err != nil {
		t.Fatal(err)
	}
	confirmed, err := pacer.Settle(ctx, "sms", late, late.Granted)
	if err != nil {
		t.Fatal(err)
	}
	// Tolerance 40 ms at 10 ms a turn leaves room for four sends at one moment, plus a
	// turn for each interval this took on a busy machine.
	if most := 5 + int(time.Since(began)/(10*time.Millisecond)); confirmed+fresh.Granted > most {
		t.Fatalf("%d sends at once, the pace allows %d", confirmed+fresh.Granted, most)
	}
}

func TestAPacerRespectsAPauseAndHasNoCatchUpBurst(t *testing.T) {
	t.Parallel()
	pacer := newPacer(t, 10*time.Millisecond)
	ctx := context.Background()
	if err := pacer.Pause(ctx, "sms", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	paused, err := pacer.Reserve(ctx, "sms", 32)
	if err != nil || paused.Granted != 0 || !paused.Paused {
		t.Fatalf("paused: %+v, %v", paused, err)
	}
	time.Sleep(350 * time.Millisecond)
	after, err := pacer.Reserve(ctx, "sms", 32)
	if err != nil || after.Granted < 1 || after.Granted > 4 {
		t.Fatalf("after the pause: %+v, %v", after, err)
	}
}

func TestAPacerFailsWhenRedisIsDown(t *testing.T) {
	t.Parallel()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer client.Close()
	if _, err := delivery.NewRedisPacer(client, 12*time.Millisecond, "x").Reserve(context.Background(), "sms", 1); err == nil {
		t.Fatal("no limiter must mean no permission to send")
	}
}

func TestAPauseHoldsEveryPacerOnThatChannelOnly(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	first, second := delivery.NewRedisPacer(client, 10*time.Millisecond, prefix), delivery.NewRedisPacer(client, 10*time.Millisecond, prefix)
	ctx := context.Background()
	if err := first.Pause(ctx, "sms", 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	permits, err := second.Reserve(ctx, "sms", 1)
	if err != nil || permits.Granted != 0 || !permits.Paused || permits.Retry < 200*time.Millisecond {
		t.Fatalf("another worker: %+v, %v", permits, err)
	}
	if permits, err := second.Reserve(ctx, "email", 1); err != nil || permits.Granted != 1 {
		t.Fatalf("email: %+v, %v", permits, err)
	}
}

// TestWorkersReturningTurnsCannotBeatThePace has workers use a random part of what they
// are given. Whatever anyone returns, the sends confirmed must stay within the pace.
func TestWorkersReturningTurnsCannotBeatThePace(t *testing.T) {
	t.Parallel()
	client := testsupport.NewRedis(t)
	prefix := testsupport.Unique(t)
	ctx := context.Background()
	const (
		interval = 5 * time.Millisecond
		duration = 2 * time.Second
	)
	began := time.Now()
	counts := make(chan int, 4)
	for seed := range uint64(4) {
		pacer := delivery.NewRedisPacer(client, interval, prefix)
		random := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // test timing
		go func() {
			total := 0
			for time.Since(began) < duration {
				permits, err := pacer.Reserve(ctx, "sms", 8)
				if err != nil {
					t.Errorf("reserve: %v", err)
					break
				}
				if permits.Granted == 0 {
					time.Sleep(permits.Retry)
					continue
				}
				confirmed, err := pacer.Settle(ctx, "sms", permits, random.IntN(permits.Granted+1))
				if err != nil {
					t.Errorf("settle: %v", err)
					break
				}
				total += confirmed
			}
			counts <- total
		}()
	}
	total := 0
	for range 4 {
		total += <-counts
	}
	// One send per interval over the time taken, plus what the tolerance may give at once.
	most := int(time.Since(began)/interval) + int(40*time.Millisecond/interval) + 1
	if total > most {
		t.Fatalf("%d sends confirmed, the pace allows %d", total, most)
	}
	if total < most/2 {
		t.Fatalf("only %d sends confirmed of %d: returned turns were lost", total, most)
	}
}
