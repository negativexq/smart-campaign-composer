package delivery

import (
	"testing"
	"time"
)

type breakerClock struct{ at time.Time }

func (c *breakerClock) now() time.Time { return c.at }

func testBreaker() (*breaker, *breakerClock) {
	clock := &breakerClock{time.Unix(1000, 0)}
	return newBreaker(BreakerConfig{Window: 10 * time.Second, MinCalls: 20, FailureRatio: 0.5, FirstWait: 5 * time.Second, MaxWait: 30 * time.Second, Probes: 3}, clock.now), clock
}

func trip(t *testing.T, b *breaker) {
	t.Helper()
	for range 20 {
		if hold, _ := b.record(true); hold > 0 {
			return
		}
	}
	t.Fatal("did not open")
}

func TestABreakerThatIsOffAllowsEverything(t *testing.T) {
	var b *breaker = newBreaker(BreakerConfig{}, time.Now)
	if allowed, _, probing := b.allow(7); allowed != 7 || probing {
		t.Fatalf("allowed %d, probing %v", allowed, probing)
	}
	if hold, closed := b.record(true); hold != 0 || closed {
		t.Fatalf("hold %v, closed %v", hold, closed)
	}
	b.giveBack(1)
}

func TestUsualFailuresDoNotOpenTheBreaker(t *testing.T) {
	b, _ := testBreaker()
	for call := range 1000 {
		if hold, _ := b.record(call%33 == 0); hold != 0 { // about 3%, as in a normal send
			t.Fatalf("opened at call %d", call)
		}
	}
}

func TestAFewFailuresAloneDoNotOpenTheBreaker(t *testing.T) {
	b, _ := testBreaker()
	for range 19 {
		if hold, _ := b.record(true); hold != 0 {
			t.Fatal("opened below the minimum number of calls")
		}
	}
}

func TestOldCallsFallOutOfTheWindow(t *testing.T) {
	b, clock := testBreaker()
	for range 19 {
		b.record(true)
	}
	clock.at = clock.at.Add(11 * time.Second)
	if hold, _ := b.record(true); hold != 0 {
		t.Fatal("counted failures from outside the window")
	}
}

func TestAnOpenBreakerAllowsNothingUntilItsWaitEnds(t *testing.T) {
	b, clock := testBreaker()
	trip(t, b)
	if allowed, retry, _ := b.allow(10); allowed != 0 || retry != 5*time.Second {
		t.Fatalf("allowed %d, retry %v", allowed, retry)
	}
	clock.at = clock.at.Add(5 * time.Second)
	allowed, _, probing := b.allow(10)
	if allowed != 3 || !probing {
		t.Fatalf("allowed %d, probing %v", allowed, probing)
	}
	if allowed, retry, _ := b.allow(10); allowed != 0 || retry != probeRecheck {
		t.Fatalf("beyond the probes: allowed %d, retry %v", allowed, retry)
	}
}

func TestProbesThatPassCloseTheBreaker(t *testing.T) {
	b, clock := testBreaker()
	trip(t, b)
	clock.at = clock.at.Add(5 * time.Second)
	b.allow(3)
	b.record(false)
	b.record(false)
	if _, closed := b.record(false); !closed {
		t.Fatal("did not close")
	}
	if allowed, _, probing := b.allow(10); allowed != 10 || probing {
		t.Fatalf("allowed %d, probing %v", allowed, probing)
	}
}

func TestAFailedProbeWaitsLongerUpToTheLimit(t *testing.T) {
	b, clock := testBreaker()
	trip(t, b)
	for _, want := range []time.Duration{10, 20, 30, 30} {
		clock.at = clock.at.Add(b.wait)
		b.allow(3)
		if hold, _ := b.record(true); hold != want*time.Second {
			t.Fatalf("hold %v, want %v", hold, want*time.Second)
		}
	}
}

func TestAProbeThatToldNothingCanGoAgain(t *testing.T) {
	b, clock := testBreaker()
	trip(t, b)
	clock.at = clock.at.Add(5 * time.Second)
	b.allow(3)
	b.giveBack(2)
	if allowed, _, _ := b.allow(10); allowed != 2 {
		t.Fatalf("allowed %d", allowed)
	}
}

func TestCallsStillOutWhenItOpenedChangeNothing(t *testing.T) {
	b, _ := testBreaker()
	trip(t, b)
	if hold, closed := b.record(false); hold != 0 || closed {
		t.Fatalf("hold %v, closed %v", hold, closed)
	}
	if allowed, _, _ := b.allow(1); allowed != 0 {
		t.Fatalf("allowed %d", allowed)
	}
}
