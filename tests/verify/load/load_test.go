//go:build verify

// Package load sends a full campaign through the verification stack under provider
// faults, lost Redis data and crashed workers, and checks what must hold every time:
// each message ends in a final state, none is accepted twice, and no channel goes
// over the provider's limit of 100 calls a second. It also records how long each
// run took, so a slowdown shows up next to the earlier results.
//
// Start the stack with `make verify-up`, then run `make verify-load`. A single
// scenario runs with VERIFY_ONLY=name (several: comma separated).
package load

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/tests/verify/stack"
)

const (
	rateLimit  = 100
	runTimeout = 25 * time.Minute
)

// faults are the mock provider's settings for one scenario.
type faults map[string]string

func healthy() faults {
	return faults{"VERIFY_429": "0", "VERIFY_5XX": "0", "VERIFY_LOST": "0", "VERIFY_401": "", "VERIFY_SLOW": "0", "VERIFY_SLOW_MIN_MS": "0", "VERIFY_SLOW_MAX_MS": "0", "RATE_LIMIT_PER_SECOND": "100"}
}

func with(name, value string) faults {
	f := healthy()
	f[name] = value
	return f
}

// run is one scenario in progress. Events record what they did in notes.
type run struct {
	campaign string
	started  time.Time
	mu       sync.Mutex
	notes    map[string]any
	// earlier holds the provider's counters from before each time it stopped. A
	// stopped provider starts again from zero, so its last counters are kept here.
	earlier []map[string]stack.ProviderStats
}

// beforeProviderStops keeps the provider's counters just before it stops. Calls in
// the moment between this read and the stop are not counted.
func (r *run) beforeProviderStops() error {
	stats, err := stack.ProviderErr()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.earlier = append(r.earlier, stats)
	return nil
}

func (r *run) note(name string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes[name] = value
}

func (r *run) at() float64 { return seconds(time.Since(r.started)) }

func seconds(d time.Duration) float64 { return float64(d.Milliseconds()/100) / 10 }

// errFinishedFirst means the campaign finished before the event it was meant to
// face, so the scenario tested nothing.
var errFinishedFirst = fmt.Errorf("the campaign finished before the event")

// sleepUntil waits until the run is the given time old.
func (r *run) sleepUntil(ctx context.Context, offset time.Duration) error {
	select {
	case <-time.After(time.Until(r.started.Add(offset))):
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w at %s", errFinishedFirst, offset)
	}
}

// event breaks something while the campaign is sending. It runs outside the test
// goroutine, so it returns an error instead of failing the test.
type event func(ctx context.Context, r *run) error

type scenario struct {
	name    string
	faults  faults
	event   event
	workers int
	// mayFail allows messages to fail after their last attempt.
	mayFail bool
	// restartsProvider marks a scenario whose provider starts again mid-run, which
	// clears its counters: it can only have accepted as many as were sent, or fewer.
	restartsProvider bool
	// wantEvents are audit events that must appear, as "type channel alert";
	// channel and alert may be empty.
	wantEvents []string
	// overLimit sets the workers' limit above the provider's, so the provider's own
	// limit is what holds: it must refuse some calls, and every message must still be
	// sent once. Calls over 100 a second at the provider are then expected.
	overLimit bool
	// beyondMargin marks a scenario that steps Redis's clock by more than the
	// limiter's 100 ms margin. Going over 100 a second is then measured and
	// reported, not failed: the docs say such a step can let calls in early.
	beyondMargin bool
	// wantLate needs slow calls that outlasted the worker's wait, or the scenario
	// tested nothing.
	wantLate bool
}

func scenarios() []scenario {
	return []scenario{
		{name: "healthy", faults: healthy()},
		{name: "default-faults", faults: func() faults {
			f := healthy()
			f["VERIFY_429"], f["VERIFY_5XX"], f["VERIFY_LOST"] = "0.02", "0.03", "0.002"
			return f
		}()},
		{name: "429-10pct", faults: with("VERIFY_429", "0.10")},
		{name: "5xx-30pct", faults: with("VERIFY_5XX", "0.30"), mayFail: true},
		{name: "lost-5pct", faults: with("VERIFY_LOST", "0.05"), mayFail: true},
		{name: "outage-40s", faults: healthy(), event: outage(20*time.Second, 40*time.Second), restartsProvider: true},
		{name: "worker-crash", faults: healthy(), event: crashWorker(20 * time.Second)},
		{
			name: "5xx70-then-ok", faults: with("VERIFY_5XX", "0.70"), event: fixProviderAt(150 * time.Second), restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened  error_rate", "campaign.alert_closed  error_rate"},
		},
		{
			name: "sms401-2workers", faults: with("VERIFY_401", "sms"), event: fixProviderAt(180 * time.Second), workers: 2, restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened sms credentials_refused", "campaign.alert_closed sms credentials_refused"},
		},
		{name: "redis-lost", faults: healthy(), event: loseRedisAt(30*time.Second, nil)},
		{name: "redis-crashed-5x", faults: healthy(), event: crashRedis(20*time.Second, 5, 12*time.Second)},
		{name: "redis-flushed-10x", faults: healthy(), event: flushRedis(20*time.Second, 10, 2*time.Second)},
		{name: "redis-flushed-10x-2workers", faults: healthy(), event: flushRedis(20*time.Second, 10, 2*time.Second), workers: 2},
		{
			name: "sms401-redis-lost", faults: with("VERIFY_401", "sms"), event: loseRedisAt(60*time.Second, fixProviderAt(150*time.Second)), restartsProvider: true,
			wantEvents: []string{"campaign.alert_opened sms credentials_refused", "campaign.alert_closed sms credentials_refused"},
		},
		{name: "workers-at-120", faults: with("RATE_LIMIT_PER_SECOND", "120"), overLimit: true},
		{name: "clock-forward-70ms", faults: healthy(), event: stepClock(70 * time.Millisecond)},
		{name: "clock-forward-100ms", faults: healthy(), event: stepClock(100 * time.Millisecond)},
		{name: "clock-forward-150ms", faults: healthy(), event: stepClock(150 * time.Millisecond), beyondMargin: true},
		{name: "clock-forward-500ms", faults: healthy(), event: stepClock(500 * time.Millisecond), beyondMargin: true},
		{name: "postgres-down-20s", faults: healthy(), event: postgresDown(25*time.Second, 20*time.Second)},
		{name: "graceful-restart", faults: healthy(), event: restartGracefully(20 * time.Second)},
		{
			name: "slow-3pct", wantLate: true,
			faults: func() faults {
				f := healthy()
				f["VERIFY_SLOW"], f["VERIFY_SLOW_MIN_MS"], f["VERIFY_SLOW_MAX_MS"] = "0.03", "3000", "8000"
				return f
			}(),
		},
		{
			name: "both401-paused", faults: with("VERIFY_401", "sms,email"), event: resumeWhenPaused, restartsProvider: true,
			wantEvents: []string{"campaign.paused  ", "campaign.resumed  "},
		},
	}
}

// result is what one scenario measured.
type result struct {
	Name          string                         `json:"name"`
	Workers       int                            `json:"workers"`
	Faults        faults                         `json:"faults"`
	Status        string                         `json:"status"`
	SendSeconds   float64                        `json:"send_seconds"`
	Started       time.Time                      `json:"started"`
	Final         map[string]map[string]int      `json:"final"`
	Outcomes      map[string]map[string]int      `json:"outcomes"`
	AcceptedTwice int                            `json:"accepted_twice"`
	PeakRecorded  map[string]int                 `json:"peak_recorded_per_second"`
	Provider      map[string]stack.ProviderStats `json:"provider"`
	// ProviderEarlier are the provider's counters from before each restart.
	ProviderEarlier []map[string]stack.ProviderStats `json:"provider_before_restarts,omitempty"`
	Events          []string                         `json:"events"`
	Notes           map[string]any                   `json:"notes,omitempty"`
	EventError      string                           `json:"event_error,omitempty"`
	Problems        []string                         `json:"problems,omitempty"`
}

func TestScenarios(t *testing.T) {
	started := time.Now()
	only := strings.Split(os.Getenv("VERIFY_ONLY"), ",")
	var results []result
	for _, s := range scenarios() {
		if only[0] != "" && !slices.Contains(only, s.name) {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			res := runScenario(t, s)
			res.Problems = check(s, res)
			for _, problem := range res.Problems {
				t.Error(problem)
			}
			peak, _ := providerTotals(res)
			t.Logf("%s: %s in %.1f s, peak at the provider email %d at %s, sms %d at %s, accepted twice %d", s.name, res.Status, res.SendSeconds,
				peak["email"].calls, peak["email"].into(res.Started), peak["sms"].calls, peak["sms"].into(res.Started), res.AcceptedTwice)
			results = append(results, res)
		})
	}
	if len(results) > 0 {
		stack.WriteResult(t, stack.Stamp("load", started), results, report(started, results))
	}
}

func runScenario(t *testing.T, s scenario) result {
	workers := max(s.workers, 1)
	// A fresh provider and fresh workers: the provider's counters start at zero.
	stack.Compose(t, s.faults, "up", "-d", "--no-deps", "--force-recreate", "--wait", "--scale", "worker="+strconv.Itoa(workers), "worker", "mock-provider")
	campaign := stack.Approved(t, stack.Brief)

	r := &run{campaign: campaign, started: time.Now(), notes: map[string]any{}}
	stack.Dispatch(t, campaign)
	ctx, cancel := context.WithCancel(context.Background())
	eventDone := make(chan error, 1)
	if s.event != nil {
		go func() { eventDone <- s.event(ctx, r) }()
	} else {
		eventDone <- nil
	}
	status := stack.WaitFinished(t, campaign, runTimeout)
	sent := time.Since(r.started)
	cancel()
	eventErr := <-eventDone

	res := result{
		Name: s.name, Workers: workers, Faults: s.faults, Status: status, SendSeconds: seconds(sent), Started: r.started,
		Final:           counts(t, `SELECT channel, status, count(*) FROM campaign_deliveries WHERE campaign_id = '%s' AND status <> 'SKIPPED' GROUP BY 1, 2`, campaign),
		Outcomes:        counts(t, `SELECT channel, outcome, count(*) FROM delivery_attempts WHERE campaign_id = '%s' GROUP BY 1, 2`, campaign),
		Provider:        stack.Provider(t),
		Notes:           r.notes,
		ProviderEarlier: r.earlier,
	}
	if eventErr != nil {
		res.EventError = eventErr.Error()
	}
	res.AcceptedTwice, _ = strconv.Atoi(stack.SQL(t, `SELECT count(*) FROM (SELECT delivery_id FROM delivery_attempts WHERE campaign_id = '%s' AND outcome = 'accepted' GROUP BY 1 HAVING count(*) > 1) x`, campaign))
	res.PeakRecorded = map[string]int{}
	for _, line := range lines(stack.SQL(t, `
		SELECT channel, max(n) FROM (
			SELECT channel, count(*) OVER (PARTITION BY channel ORDER BY occurred_at RANGE BETWEEN interval '999999 microseconds' PRECEDING AND CURRENT ROW) n
			FROM delivery_attempts WHERE campaign_id = '%s') x GROUP BY 1`, campaign)) {
		fields := strings.Split(line, "|")
		res.PeakRecorded[fields[0]], _ = strconv.Atoi(fields[1])
	}
	res.Events = lines(stack.SQL(t, `
		SELECT event_type || ' ' || coalesce(details->>'channel', '') || ' ' || coalesce(details->>'alert', '')
		FROM audit_events WHERE campaign_id = '%s'
		AND event_type IN ('campaign.alert_opened', 'campaign.alert_closed', 'campaign.paused', 'campaign.resumed')
		ORDER BY id`, campaign))
	return res
}

// check lists what went wrong in a run. Each rule holds in every scenario unless
// the scenario says otherwise.
func check(s scenario, res result) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if res.EventError != "" {
		add("the event failed or did not happen as planned: %s", res.EventError)
	}
	if !strings.HasPrefix(res.Status, "COMPLETED") {
		add("the campaign ended %s", res.Status)
	}
	for channel, statuses := range res.Final {
		for status, n := range statuses {
			switch {
			case status == "SENT":
			case status == "FAILED" && s.mayFail:
			default:
				add("%d %s messages ended %s", n, channel, status)
			}
		}
	}
	if res.AcceptedTwice != 0 {
		add("%d messages were accepted twice", res.AcceptedTwice)
	}
	total := 0
	for _, statuses := range res.Final {
		for _, n := range statuses {
			total += n
		}
	}
	if total == 0 {
		add("the campaign had no messages")
	}
	if s.restartsProvider && len(res.ProviderEarlier) == 0 {
		add("the provider was meant to restart but its counters from before were not kept")
	}
	peak, accepted := providerTotals(res)
	throttled := sum(res, func(stats stack.ProviderStats) int { return stats.Throttled })
	switch {
	case s.overLimit && throttled == 0:
		add("the workers sent over the provider's limit, but it refused nothing")
	case !s.overLimit && !s.beyondMargin && throttled > 0:
		add("the provider refused %d calls for going over its limit", throttled)
	}
	if s.wantLate && late(res) == 0 {
		add("no slow call outlasted the worker's wait")
	}
	for channel, statuses := range res.Final {
		if peak[channel].calls > rateLimit && !s.overLimit && !s.beyondMargin {
			add("%s reached %d calls in one second at the provider, over %d, %s into the run", channel, peak[channel].calls, rateLimit, peak[channel].into(res.Started))
		}
		// Each message the campaign counts as sent was accepted by the provider once.
		// Across a restart, calls between the last read and the stop go uncounted, so
		// the provider may have counted fewer, never more.
		sent := statuses["SENT"]
		if accepted[channel] > sent || (!s.restartsProvider && accepted[channel] != sent) {
			add("the provider accepted %d %s messages, the campaign sent %d", accepted[channel], channel, sent)
		}
	}
	for _, want := range s.wantEvents {
		if !slices.ContainsFunc(res.Events, func(got string) bool { return matches(got, want) }) {
			add("no %q event; events: %v", want, res.Events)
		}
	}
	return problems
}

// busiest is a channel's highest one-second count at the provider and when it was.
type busiest struct {
	calls int
	at    *time.Time
}

// into says how far into the run the busiest second ended.
func (b busiest) into(started time.Time) string {
	if b.at == nil || started.IsZero() {
		return "at an unknown time"
	}
	return fmt.Sprintf("%.1f s", b.at.Sub(started).Seconds())
}

// providerTotals adds up the provider's counters across its restarts: the highest
// one-second peak and every message it accepted.
func providerTotals(res result) (peak map[string]busiest, accepted map[string]int) {
	peak, accepted = map[string]busiest{}, map[string]int{}
	for _, part := range append(slices.Clone(res.ProviderEarlier), res.Provider) {
		for channel, stats := range part {
			if stats.PeakPerSecond > peak[channel].calls {
				peak[channel] = busiest{stats.PeakPerSecond, stats.PeakAt}
			}
			accepted[channel] += stats.Accepted
		}
	}
	return peak, accepted
}

// late counts the slow calls that outlasted the worker's wait, across restarts.
func late(res result) int {
	return sum(res, func(stats stack.ProviderStats) int { return stats.Late })
}

// sum adds up one of the provider's counters over both channels and its restarts.
func sum(res result, counter func(stack.ProviderStats) int) int {
	n := 0
	for _, part := range append(slices.Clone(res.ProviderEarlier), res.Provider) {
		for _, stats := range part {
			n += counter(stats)
		}
	}
	return n
}

// matches compares an event with a wanted one, where an empty channel or alert in
// the wanted event matches any.
func matches(got, want string) bool {
	g, w := strings.Split(got, " "), strings.Split(want, " ")
	for i := range w {
		if w[i] != "" && (i >= len(g) || g[i] != w[i]) {
			return false
		}
	}
	return true
}

func counts(t *testing.T, query string, campaign string) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, line := range lines(stack.SQL(t, query, campaign)) {
		fields := strings.Split(line, "|")
		n, _ := strconv.Atoi(fields[2])
		if out[fields[0]] == nil {
			out[fields[0]] = map[string]int{}
		}
		out[fields[0]][fields[1]] = n
	}
	return out
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// The events.

// fixProviderAt restarts the provider without faults once the run is that old.
func fixProviderAt(offset time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		if err := r.beforeProviderStops(); err != nil {
			return err
		}
		r.note("fixed_at", r.at())
		_, err := stack.ComposeErr(healthy(), "up", "-d", "--no-deps", "--force-recreate", "--wait", "mock-provider")
		return err
	}
}

// outage stops the provider for a while.
func outage(offset, length time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		if err := r.beforeProviderStops(); err != nil {
			return err
		}
		if _, err := stack.DockerErr("stop", "-t", "0", stack.Container("mock-provider")); err != nil {
			return err
		}
		r.note("down_at", r.at())
		time.Sleep(length)
		_, err := stack.DockerErr("start", stack.Container("mock-provider"))
		r.note("up_at", r.at())
		return err
	}
}

// crashWorker kills the worker until a kill leaves messages mid-send, then starts
// it again: those messages must still end once, and only once.
func crashWorker(offset time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		for kill := 1; kill <= 10; kill++ {
			if _, err := stack.DockerErr("kill", stack.Container("worker")); err != nil {
				return err
			}
			orphans, err := stack.DockerErr("exec", stack.Container("postgres"), "psql", "-U", "scc", "-d", "scc", "-At", "-c",
				fmt.Sprintf(`SELECT count(*) FROM campaign_deliveries WHERE campaign_id = '%s' AND status = 'SENDING'`, r.campaign))
			if err != nil {
				return err
			}
			if _, err := stack.DockerErr("start", stack.Container("worker")); err != nil {
				return err
			}
			if orphans != "0" {
				r.note("kills", kill)
				r.note("left_mid_send", orphans)
				return nil
			}
			time.Sleep(4 * time.Second)
		}
		return fmt.Errorf("no kill left a message mid-send")
	}
}

// postgresDown kills PostgreSQL, keeps it down for a while and starts it again: it
// recovers from its log as after a crash. Messages mid-send while it is gone must
// still end once, and only once.
func postgresDown(offset, length time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		postgres := stack.Container("postgres")
		if _, err := stack.DockerErr("kill", postgres); err != nil {
			return err
		}
		r.note("postgres_killed_at", r.at())
		time.Sleep(length)
		if _, err := stack.DockerErr("start", postgres); err != nil {
			return err
		}
		for range 150 {
			if _, err := stack.DockerErr("exec", postgres, "pg_isready", "-U", "scc"); err == nil {
				r.note("postgres_back_at", r.at())
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("postgres did not come back")
	}
}

// restartGracefully stops the worker the way a deploy does, with SIGTERM and the
// grace period compose gives it, then restarts the API too. The worker must finish
// what it started and exit by itself: no message may be left mid-send, and it must
// not need killing.
func restartGracefully(offset time.Duration) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		worker := stack.Container("worker")
		stopping := time.Now()
		if _, err := stack.DockerErr("stop", "-t", "30", worker); err != nil {
			return err
		}
		r.note("worker_stop_seconds", seconds(time.Since(stopping)))
		code, err := stack.DockerErr("inspect", "-f", "{{.State.ExitCode}}", worker)
		if err != nil {
			return err
		}
		r.note("worker_exit_code", code)
		midSend, err := stack.DockerErr("exec", stack.Container("postgres"), "psql", "-U", "scc", "-d", "scc", "-At", "-c",
			fmt.Sprintf(`SELECT count(*) FROM campaign_deliveries WHERE campaign_id = '%s' AND status = 'SENDING'`, r.campaign))
		if err != nil {
			return err
		}
		r.note("left_mid_send", midSend)
		if _, err := stack.DockerErr("start", worker); err != nil {
			return err
		}
		if _, err := stack.DockerErr("restart", "-t", "30", stack.Container("api")); err != nil {
			return err
		}
		r.note("api_restarted_at", r.at())
		switch {
		case code != "0":
			return fmt.Errorf("the worker exited with %s instead of stopping by itself", code)
		case midSend != "0":
			return fmt.Errorf("the worker stopped with %s messages mid-send", midSend)
		}
		return nil
	}
}

// stepClock moves the wall clock of Docker's VM forward by the given amount five
// times, 10 s apart from 20 s, as a time service that steps instead of slewing
// would. Every container shares that clock, Redis's included. Each step is made by
// tests/verify/clockstep in a privileged container on the stack's Redis image.
func stepClock(by time.Duration) event {
	return func(ctx context.Context, r *run) error {
		arch, err := stack.DockerErr("version", "-f", "{{.Server.Arch}}")
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "clockstep")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		build := exec.Command("go", "build", "-o", filepath.Join(dir, "clockstep"), "./tests/verify/clockstep")
		build.Dir = stack.Root()
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build clockstep: %v: %s", err, out)
		}
		image, err := stack.DockerErr("inspect", "-f", "{{.Config.Image}}", stack.Container("redis"))
		if err != nil {
			return err
		}
		var steps []string
		defer func() { r.note("clock_steps", steps) }()
		for i := range 5 {
			if err := r.sleepUntil(ctx, 20*time.Second+time.Duration(i)*10*time.Second); err != nil {
				return err
			}
			moved, err := stack.DockerErr("run", "--rm", "--privileged", "-v", dir+":/tool:ro", image, "/tool/clockstep", by.String())
			if err != nil {
				return err
			}
			steps = append(steps, fmt.Sprintf("%s at %.1f s", moved, r.at()))
		}
		return nil
	}
}

// loseRedisAt kills Redis, starts it empty, then runs next if given.
func loseRedisAt(offset time.Duration, next event) event {
	return func(ctx context.Context, r *run) error {
		if err := r.sleepUntil(ctx, offset); err != nil {
			return err
		}
		redis := stack.Container("redis")
		r.note("redis_killed_at", r.at())
		if _, err := stack.DockerErr("kill", redis); err != nil {
			return err
		}
		time.Sleep(3 * time.Second)
		if _, err := stack.DockerErr("start", redis); err != nil {
			return err
		}
		for range 50 {
			if pong, _ := stack.DockerErr("exec", redis, "redis-cli", "ping"); pong == "PONG" {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if _, err := stack.DockerErr("exec", redis, "redis-cli", "FLUSHALL"); err != nil {
			return err
		}
		r.note("redis_back_at", r.at())
		if next != nil {
			return next(ctx, r)
		}
		return nil
	}
}

// crashRedis kills Redis count times, every interval apart, and starts it again at
// once, without emptying it: it comes back with whatever it saved to disk last, as
// after a crash or an out-of-memory kill.
func crashRedis(offset time.Duration, count int, every time.Duration) event {
	return func(ctx context.Context, r *run) error {
		redis := stack.Container("redis")
		var crashes []string
		defer func() { r.note("crashes", crashes) }()
		for i := range count {
			if err := r.sleepUntil(ctx, offset+time.Duration(i)*every); err != nil {
				return err
			}
			saved, _ := stack.DockerErr("exec", redis, "redis-cli", "lastsave")
			killed := time.Now()
			if _, err := stack.DockerErr("kill", redis); err != nil {
				return err
			}
			if _, err := stack.DockerErr("start", redis); err != nil {
				return err
			}
			for range 100 {
				if pong, _ := stack.DockerErr("exec", redis, "redis-cli", "ping"); pong == "PONG" {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			age := "unknown"
			if at, err := strconv.ParseInt(saved, 10, 64); err == nil {
				age = fmt.Sprintf("%.0f s", killed.Sub(time.Unix(at, 0)).Seconds())
			}
			crashes = append(crashes, fmt.Sprintf("at %.1f s, down %.1f s, LASTSAVE (last snapshot, or start without snapshots) %s before", seconds(killed.Sub(r.started)), seconds(time.Since(killed)), age))
		}
		return nil
	}
}

// flushRedis empties Redis count times, every interval apart.
func flushRedis(offset time.Duration, count int, every time.Duration) event {
	return func(ctx context.Context, r *run) error {
		var at []float64
		defer func() { r.note("flushed_at", at) }()
		for i := range count {
			if err := r.sleepUntil(ctx, offset+time.Duration(i)*every); err != nil {
				return err
			}
			if _, err := stack.DockerErr("exec", stack.Container("redis"), "redis-cli", "FLUSHALL"); err != nil {
				return err
			}
			at = append(at, r.at())
		}
		return nil
	}
}

// resumeWhenPaused waits for the campaign to pause on refused credentials, fixes the
// provider and resumes it.
func resumeWhenPaused(ctx context.Context, r *run) error {
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(method, path string) (int, string, error) {
		request, err := http.NewRequestWithContext(ctx, method, stack.APIURL()+path, strings.NewReader("{}"))
		if err != nil {
			return 0, "", err
		}
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(stack.Session)
		response, err := client.Do(request)
		if err != nil {
			return 0, "", err
		}
		defer response.Body.Close()
		var body struct{ Status string }
		_ = jsonDecode(response, &body)
		return response.StatusCode, body.Status, nil
	}
	for {
		_, status, err := call(http.MethodGet, "/api/campaigns/"+r.campaign)
		if err != nil {
			return err
		}
		if status == "PAUSED" {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the campaign never paused")
		case <-time.After(2 * time.Second):
		}
	}
	r.note("paused_at", r.at())
	if err := r.beforeProviderStops(); err != nil {
		return err
	}
	if _, err := stack.ComposeErr(healthy(), "up", "-d", "--no-deps", "--force-recreate", "--wait", "mock-provider"); err != nil {
		return err
	}
	code, _, err := call(http.MethodPost, "/api/campaigns/"+r.campaign+"/resume")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("resume: %d", code)
	}
	r.note("resumed_at", r.at())
	return nil
}

func jsonDecode(response *http.Response, value any) error {
	return json.NewDecoder(response.Body).Decode(value)
}

// report writes the runs as a table, one row per scenario.
func report(started time.Time, results []result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Load verification, %s\n\n", started.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("Each run sends the same campaign through the verification stack. Peak is the most calls the mock provider saw in one second, per channel, refused ones included; the limit is 100. The provider also enforces that limit itself and refuses calls over it with a 429.\n\n")
	b.WriteString("| Scenario | Workers | Status | Send time | Sent | Failed | Accepted twice | Peak email / sms | Refused over the provider's limit | Events | Problems |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		peak, _ := providerTotals(r)
		sent, failed := 0, 0
		for _, statuses := range r.Final {
			sent += statuses["SENT"]
			failed += statuses["FAILED"]
		}
		problems := "none"
		if len(r.Problems) > 0 {
			problems = strings.Join(r.Problems, "; ")
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %.1f s | %d | %d | %d | %d / %d | %d | %s | %s |\n", r.Name, r.Workers, r.Status, r.SendSeconds, sent, failed, r.AcceptedTwice,
			peak["email"].calls, peak["sms"].calls, sum(r, func(stats stack.ProviderStats) int { return stats.Throttled }), strings.Join(r.Events, ", "), problems)
	}
	return b.String()
}
