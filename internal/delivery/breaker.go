package delivery

import (
	"sync"
	"time"
)

// probeRecheck is how soon to ask again while the probes are still out.
const probeRecheck = 100 * time.Millisecond

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerProbing
)

type breakerCall struct {
	at     time.Time
	failed bool
}

// breaker stops a channel from calling a provider that is failing, so messages wait
// in the queue instead of using up their attempts on a problem that is not theirs.
// It is closed while calls go out, open while none do, and after the wait it lets a
// few probes through to see whether the provider is back. A nil breaker is off.
type breaker struct {
	config BreakerConfig
	now    func() time.Time

	mu     sync.Mutex
	state  breakerState
	calls  []breakerCall
	until  time.Time
	wait   time.Duration
	probes int
	passed int
}

func newBreaker(config BreakerConfig, now func() time.Time) *breaker {
	if config.MinCalls <= 0 {
		return nil
	}
	return &breaker{config: config, now: now}
}

// allow says how many of the wanted calls may go out, how long to wait if none may,
// and whether they are probes.
func (b *breaker) allow(want int) (int, time.Duration, bool) {
	if b == nil {
		return want, 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		return want, 0, false
	case breakerOpen:
		if remaining := b.until.Sub(b.now()); remaining > 0 {
			return 0, remaining, false
		}
		b.state, b.probes, b.passed = breakerProbing, b.config.Probes, 0
	}
	allowed := min(want, b.probes)
	b.probes -= allowed
	if allowed == 0 {
		return 0, probeRecheck, false
	}
	return allowed, 0, true
}

// giveBack returns calls that never reached the provider or told nothing about it.
func (b *breaker) giveBack(count int) {
	if b == nil || count <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == breakerProbing {
		b.probes += count
	}
}

// record takes a call's result. It returns how long to hold the channel if this
// opened the breaker, and whether this closed it.
func (b *breaker) record(failed bool) (hold time.Duration, closed bool) {
	if b == nil {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case breakerClosed:
		return b.recordClosed(failed), false
	case breakerProbing:
		if failed {
			return b.open(min(b.wait*2, b.config.MaxWait)), false
		}
		b.passed++
		if b.passed >= b.config.Probes {
			b.state, b.calls, b.wait = breakerClosed, nil, 0
			return 0, true
		}
	}
	// A call still out when the breaker opened says nothing new.
	return 0, false
}

func (b *breaker) recordClosed(failed bool) time.Duration {
	now := b.now()
	b.calls = append(b.calls, breakerCall{now, failed})
	first, failures := 0, 0
	for first < len(b.calls) && now.Sub(b.calls[first].at) >= b.config.Window {
		first++
	}
	b.calls = b.calls[first:]
	for _, call := range b.calls {
		if call.failed {
			failures++
		}
	}
	if len(b.calls) < b.config.MinCalls || float64(failures)/float64(len(b.calls)) < b.config.FailureRatio {
		return 0
	}
	return b.open(b.config.FirstWait)
}

func (b *breaker) open(wait time.Duration) time.Duration {
	b.state, b.calls, b.wait, b.until = breakerOpen, nil, wait, b.now().Add(wait)
	return wait
}
