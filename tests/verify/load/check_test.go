//go:build verify

package load

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/tests/verify/stack"
)

// The checks themselves: each rule must report a run that breaks it, so a load run
// that passes means the rules held, not that nothing was checked. Needs no stack:
// go test -tags=verify -run TestTheChecks ./tests/verify/load/
func TestTheChecks(t *testing.T) {
	good := func() result {
		return result{
			Status: "COMPLETED",
			Final:  map[string]map[string]int{"email": {"SENT": 10}, "sms": {"SENT": 8}},
			Provider: map[string]stack.ProviderStats{
				"email": {Accepted: 10, PeakPerSecond: 100},
				"sms":   {Accepted: 8, PeakPerSecond: 90},
			},
			Events: []string{"campaign.alert_opened sms credentials_refused", "campaign.alert_closed sms credentials_refused"},
		}
	}
	plain := scenario{name: "plain"}
	restarted := scenario{name: "restarted", restartsProvider: true}
	alerted := scenario{name: "alerted", wantEvents: []string{"campaign.alert_opened sms credentials_refused"}}

	if problems := check(plain, good()); len(problems) != 0 {
		t.Fatalf("a good run has problems: %v", problems)
	}
	if problems := check(alerted, good()); len(problems) != 0 {
		t.Fatalf("a good run with its alert has problems: %v", problems)
	}

	cases := []struct {
		name     string
		scenario scenario
		change   func(*result)
		want     string
	}{
		{"not finished", plain, func(r *result) { r.Status = "DISPATCHING" }, "ended DISPATCHING"},
		{"a message left queued", plain, func(r *result) { r.Final["sms"]["QUEUED"] = 1 }, "ended QUEUED"},
		{"a failure where none may fail", plain, func(r *result) { r.Final["sms"]["FAILED"] = 1 }, "ended FAILED"},
		{"accepted twice", plain, func(r *result) { r.AcceptedTwice = 1 }, "accepted twice"},
		{"over the limit", plain, func(r *result) { r.Provider["email"] = stack.ProviderStats{Accepted: 10, PeakPerSecond: 101} }, "over 100"},
		{"over the limit before a restart", restarted, func(r *result) {
			r.ProviderEarlier = []map[string]stack.ProviderStats{{"sms": {Accepted: 3, PeakPerSecond: 150}}}
			r.Provider["sms"] = stack.ProviderStats{Accepted: 5, PeakPerSecond: 40}
		}, "150 calls"},
		{"the provider accepted fewer", plain, func(r *result) { r.Provider["sms"] = stack.ProviderStats{Accepted: 7} }, "accepted 7 sms"},
		{"the provider accepted more across a restart", restarted, func(r *result) {
			r.ProviderEarlier = []map[string]stack.ProviderStats{{"sms": {Accepted: 5}}}
		}, "accepted 13 sms"},
		{"a restart with no counters kept", restarted, func(r *result) {}, "counters from before"},
		{"no messages", plain, func(r *result) { r.Final = map[string]map[string]int{} }, "no messages"},
		{"a missing event", scenario{name: "paused", wantEvents: []string{"campaign.paused  "}}, func(r *result) {}, `no "campaign.paused  " event`},
		{"an event that did not happen", plain, func(r *result) { r.EventError = "finished first" }, "did not happen as planned"},
	}
	for _, c := range cases {
		r := good()
		c.change(&r)
		problems := strings.Join(check(c.scenario, r), "; ")
		if !strings.Contains(problems, c.want) {
			t.Errorf("%s: want a problem with %q, got %q", c.name, c.want, problems)
		}
	}

	// A wanted event leaves channel or alert open only where it says so.
	if matches("campaign.alert_opened email error_rate", "campaign.alert_opened sms error_rate") {
		t.Error("an alert on email matched one wanted on sms")
	}
	if !matches("campaign.alert_opened email error_rate", "campaign.alert_opened  error_rate") {
		t.Error("an alert on any channel did not match")
	}
}
