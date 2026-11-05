package mockprovider

import (
	"testing"
	"time"
)

func TestTheRateCountsCallsByWhenTheyCameIn(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }

	provider := New(Behavior{})
	// A call that waited behind a slow one is recorded last but came in second.
	for _, ms := range []int{0, 900, 400} {
		provider.arrived("sms", at(ms))
	}
	if got := provider.Stats()["sms"].PeakPerSecond; got != 3 {
		t.Fatalf("three calls within a second counted as %d", got)
	}

	spread := New(Behavior{})
	// Recorded together, but a second apart when they came in.
	for _, ms := range []int{1000, 0, 2000} {
		spread.arrived("email", at(ms))
	}
	if got := spread.Stats()["email"].PeakPerSecond; got != 1 {
		t.Fatalf("calls a second apart counted as %d in one second", got)
	}
}
