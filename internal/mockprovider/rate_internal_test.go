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

func TestTheProviderRefusesCallsOverItsOwnLimit(t *testing.T) {
	t.Parallel()
	start := time.Now()
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	provider := New(Behavior{RatePerSecond: 3})

	for i, ms := range []int{0, 100, 200} {
		if !provider.admit("sms", at(ms)) {
			t.Fatalf("call %d within the limit was refused", i+1)
		}
	}
	if provider.admit("sms", at(999)) {
		t.Fatal("a fourth call within the same second was let in")
	}
	if !provider.admit("email", at(999)) {
		t.Fatal("a call on the other channel was refused")
	}
	// A refused call does not count, so the second after the first call is open.
	if !provider.admit("sms", at(1000)) {
		t.Fatal("a call a second after the first was refused")
	}
	// A call that came in earlier but is checked late still counts where it belongs.
	if provider.admit("sms", at(150)) {
		t.Fatal("a late-checked call made four in one second")
	}
	if !New(Behavior{}).admit("sms", at(0)) {
		t.Fatal("no limit set, yet a call was refused")
	}
}
