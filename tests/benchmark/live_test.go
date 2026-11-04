//go:build benchmark

package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/matching"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

// What became of a run. Only silent_wrong and forbidden_text can reach a person's
// phone unnoticed, so they are the numbers that matter most.
const (
	outcomeOK          = "ok"
	outcomeSilentWrong = "silent_wrong"
	outcomeFalseBlock  = "false_block"
	outcomeVisibleMiss = "visible_miss"
	outcomeMissedNote  = "missed_note"
	outcomeForbidden   = "forbidden_text"
	outcomeSilentSkip  = "silent_skip"
	outcomeRejected    = "rejected"
	outcomeSetupFailed = "setup_failed"
	outcomeError       = "provider_error"
)

type result struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Holdout  bool   `json:"holdout"`
	Brief    string `json:"brief"`
	Ask      string `json:"refinement_or_instruction,omitempty"`
	Run      int    `json:"run"`
	Outcome  string `json:"outcome"`
	Detail   any    `json:"detail,omitempty"`
	// Repairs is how often the backend sent an answer back before this result.
	Repairs []string `json:"repairs,omitempty"`
	Seconds float64  `json:"seconds"`
}

// watched counts what the backend sent back to the model for one case.
type watched struct {
	ai.Provider
	mu      sync.Mutex
	repairs []string
}

func (w *watched) note(reason string) {
	if reason == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.repairs = append(w.repairs, reason)
}

func (w *watched) ProposeSegment(ctx context.Context, request ai.SegmentRequest) (ai.Response, error) {
	w.note(request.ValidationError)
	return w.Provider.ProposeSegment(ctx, request)
}

func (w *watched) StreamContent(ctx context.Context, request ai.ContentRequest, handler ai.StreamHandler) (ai.Response, error) {
	w.note(request.ValidationError)
	return w.Provider.StreamContent(ctx, request, handler)
}

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func TestBenchmark(t *testing.T) {
	cases, err := Load("cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if only := os.Getenv("BENCHMARK_ONLY"); only != "" {
		pattern := regexp.MustCompile(only)
		cases = slices.DeleteFunc(cases, func(c Case) bool { return !pattern.MatchString(c.ID) })
	}
	provider, err := ai.NewFromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	pool := testsupport.NewDatabase(t)
	now := time.Now().UTC()
	if _, err := seed.Apply(context.Background(), pool, seed.Generate(now, 2000)); err != nil {
		t.Fatal(err)
	}
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	serviceFor := func(p ai.Provider) *campaign.Service {
		return campaign.NewService(pool, p, metrics, noop.NewTracerProvider().Tracer("benchmark"), func() time.Time { return now })
	}

	// One case in four is kept for checking, so tuning on the rest can be seen for what it is.
	holdout := map[string]bool{}
	perCategory := map[string]int{}
	for _, c := range cases {
		holdout[c.ID] = perCategory[c.Category]%4 == 3
		perCategory[c.Category]++
	}

	repeats := envInt("BENCHMARK_REPEATS", 1)
	var (
		mu      sync.Mutex
		results []result
		wg      sync.WaitGroup
		slots   = make(chan struct{}, envInt("BENCHMARK_CONCURRENCY", 6))
	)
	for run := 1; run <= repeats; run++ {
		for _, c := range cases {
			wg.Add(1)
			slots <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				spy := &watched{Provider: provider}
				start := time.Now()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				var outcome, ask string
				var detail any
				if c.Kind == "content" {
					ask = c.Instruction
					outcome, detail = runContent(ctx, serviceFor(spy), c)
				} else {
					ask = c.Refinement
					outcome, detail = runRule(ctx, serviceFor(spy), c)
				}
				mu.Lock()
				defer mu.Unlock()
				results = append(results, result{c.ID, c.Category, holdout[c.ID], c.Brief, ask, run, outcome, detail, spy.repairs, time.Since(start).Seconds()})
				if outcome != outcomeOK {
					t.Logf("%s run %d: %s %v", c.ID, run, outcome, detail)
				}
			}()
		}
	}
	wg.Wait()
	slices.SortFunc(results, func(a, b result) int {
		return strings.Compare(fmt.Sprintf("%s-%d", a.ID, a.Run), fmt.Sprintf("%s-%d", b.ID, b.Run))
	})
	if err := writeReport(provider, results, repeats); err != nil {
		t.Fatal(err)
	}
}

func accepted(c Case, spec segment.Spec) bool {
	if c.Rule != nil && c.Rule.Matches(spec) {
		return true
	}
	return slices.ContainsFunc(c.Alts, func(rule Rule) bool { return rule.Matches(spec) })
}

func runRule(ctx context.Context, service *campaign.Service, c Case) (string, any) {
	id, err := service.Create(ctx, c.Brief)
	if err != nil {
		return outcomeSetupFailed, err.Error()
	}
	view, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{})
	if c.Kind == "refine" && err == nil {
		if view.Proposal == nil || c.First == nil || !c.First.Matches(view.Proposal.Segment) {
			return outcomeSetupFailed, "the first rule was not the expected one"
		}
		view, err = service.ProposeSegment(ctx, id, campaign.ProposeInput{Refinement: c.Refinement})
	}
	if errors.Is(err, campaign.ErrProposalRejected) {
		if c.Expect == ExpectPass {
			return outcomeFalseBlock, "rejected: " + err.Error()
		}
		return outcomeOK, "rejected"
	}
	if err != nil {
		return outcomeError, err.Error()
	}
	proposal := view.Proposal
	detail := map[string]any{"rule": proposal.Segment, "unsupported": proposal.UnsupportedConditions, "unmatched": proposal.Unmatched, "unsure": proposal.Unsure, "not_applied": proposal.NotApplied, "not_audience": proposal.NotAudience, "brief": view.Brief}
	blocked := len(proposal.UnsupportedConditions) > 0 || len(proposal.Unmatched) > 0 || proposal.Segment.IsEmpty()
	matches := accepted(c, proposal.Segment)
	// A blocked result is already in front of a person, so it needs no note as well.
	if c.Noted != nil && *c.Noted && len(proposal.NotApplied) == 0 && !blocked {
		return outcomeMissedNote, detail
	}
	switch {
	case blocked && c.Expect == ExpectPass:
		return outcomeFalseBlock, detail
	case blocked:
		return outcomeOK, detail
	case c.Expect != ExpectBlock && matches:
		return outcomeOK, detail
	case len(proposal.NotApplied) > 0 && c.Kind == "refine":
		return outcomeVisibleMiss, detail
	case len(proposal.Unsure) > 0:
		return outcomeVisibleMiss, detail
	}
	return outcomeSilentWrong, detail
}

func notes(view campaign.View) []string {
	found := []string{}
	for _, warning := range view.ContentWarnings {
		if warning.Reason == "not_applied" {
			found = append(found, warning.Message)
		}
	}
	return found
}

func generate(ctx context.Context, service *campaign.Service, id string, input campaign.GenerateInput) (campaign.View, error) {
	generation, err := service.StartGeneration(ctx, id, input)
	if err != nil {
		return campaign.View{}, err
	}
	return generation.Run(ctx, func(campaign.GenerationEvent) {})
}

func runContent(ctx context.Context, service *campaign.Service, c Case) (string, any) {
	id, err := service.Create(ctx, c.Brief)
	if err != nil {
		return outcomeSetupFailed, err.Error()
	}
	view, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{})
	if err != nil {
		return outcomeSetupFailed, err.Error()
	}
	if len(view.Blockers) > 0 {
		return outcomeSetupFailed, view.Blockers
	}
	input := campaign.GenerateInput{Instruction: c.Instruction, Channel: c.Channel}
	if c.Channel != "" {
		if view, err = generate(ctx, service, id, campaign.GenerateInput{}); err != nil {
			return outcomeSetupFailed, err.Error()
		}
	}
	view, err = generate(ctx, service, id, input)
	if c.Refused {
		if errors.Is(err, campaign.ErrChannelDiscount) {
			return outcomeOK, "refused"
		}
		return outcomeSilentWrong, fmt.Sprintf("not refused: %v", err)
	}
	if errors.Is(err, campaign.ErrContentRejected) {
		return outcomeRejected, err.Error()
	}
	if err != nil {
		return outcomeError, err.Error()
	}
	written := view.Content.Content
	texts := map[string]string{
		"sms":   written.SMS,
		"email": strings.Join([]string{written.EmailSubject, written.EmailPreheader, written.EmailHeadline, written.EmailBody, written.EmailCTALabel, written.EmailCTAURL}, "\n"),
	}
	detail := map[string]any{"sms": written.SMS, "subject": written.EmailSubject, "body": written.EmailBody, "url": written.EmailCTAURL, "notes": notes(view)}
	for _, text := range texts {
		for _, word := range c.MustNot {
			if strings.Contains(matching.Key(text), matching.Key(word)) {
				return outcomeForbidden, detail
			}
		}
	}
	noted := len(notes(view)) > 0
	for _, text := range texts {
		for _, word := range c.Must {
			if !strings.Contains(matching.Key(text), matching.Key(word)) {
				if noted {
					return outcomeVisibleMiss, detail
				}
				return outcomeSilentSkip, detail
			}
		}
	}
	if c.Noted != nil && *c.Noted && !noted {
		return outcomeMissedNote, detail
	}
	return outcomeOK, detail
}

func writeReport(provider ai.Provider, results []result, repeats int) error {
	directory := "results"
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	stem := filepath.Join(directory, fmt.Sprintf("%s-%s-%s", time.Now().Format("2006-01-02-1504"), provider.Name(), provider.Model()))
	encoded, err := json.MarshalIndent(results, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(stem+".json", encoded, 0o644); err != nil {
		return err
	}

	outcomes := []string{outcomeOK, outcomeSilentWrong, outcomeForbidden, outcomeSilentSkip, outcomeFalseBlock, outcomeVisibleMiss, outcomeMissedNote, outcomeRejected, outcomeSetupFailed, outcomeError}
	var report strings.Builder
	fmt.Fprintf(&report, "# Benchmark: %s %s\n\n%s, %d runs of %d cases.\n\n", provider.Name(), provider.Model(), time.Now().Format("2006-01-02 15:04"), repeats, len(results)/repeats)
	table := func(title string, keep func(result) bool, group func(result) string) {
		counts := map[string]map[string]int{}
		groups := []string{}
		for _, item := range results {
			if !keep(item) {
				continue
			}
			name := group(item)
			if counts[name] == nil {
				counts[name] = map[string]int{}
				groups = append(groups, name)
			}
			counts[name][item.Outcome]++
		}
		slices.Sort(groups)
		fmt.Fprintf(&report, "## %s\n\n| |%s|\n|---|%s\n", title, strings.Join(outcomes, "|"), strings.Repeat("---|", len(outcomes)))
		for _, name := range groups {
			fmt.Fprintf(&report, "|%s|", name)
			for _, outcome := range outcomes {
				fmt.Fprintf(&report, "%d|", counts[name][outcome])
			}
			report.WriteString("\n")
		}
		report.WriteString("\n")
	}
	all := func(result) bool { return true }
	table("Overall", all, func(result) string { return "all" })
	table("Tuning and held-out cases", all, func(item result) string {
		if item.Holdout {
			return "held out"
		}
		return "tuning"
	})
	table("By category", all, func(item result) string { return item.Category })

	report.WriteString("## What the backend sent back to the model\n\n")
	repaired, firstTry := 0, 0
	for _, item := range results {
		if len(item.Repairs) > 0 {
			repaired++
			fmt.Fprintf(&report, "- `%s` %s\n", item.ID, firstLine(item.Repairs[0]))
		} else {
			firstTry++
		}
	}
	fmt.Fprintf(&report, "\n%d runs needed a repair, %d did not.\n\n", repaired, firstTry)

	if repeats > 1 {
		report.WriteString("## Different outcomes across runs\n\n")
		seen := map[string][]string{}
		for _, item := range results {
			seen[item.ID] = append(seen[item.ID], item.Outcome)
		}
		ids := []string{}
		for id, runs := range seen {
			slices.Sort(runs)
			if len(slices.Compact(slices.Clone(runs))) > 1 {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			fmt.Fprintf(&report, "- `%s`: %s\n", id, strings.Join(seen[id], ", "))
		}
		report.WriteString("\n")
	}

	report.WriteString("## Everything that was not ok\n\n")
	for _, outcome := range outcomes[1:] {
		for _, item := range results {
			if item.Outcome == outcome {
				detail, _ := json.Marshal(item.Detail)
				fmt.Fprintf(&report, "- **%s** `%s` %q %s\n  - %s\n", outcome, item.ID, item.Brief, quoted(item.Ask), detail)
			}
		}
	}
	return os.WriteFile(stem+".md", []byte(report.String()), 0o644)
}

func firstLine(text string) string {
	if line, _, found := strings.Cut(text, "\n"); found {
		return line
	}
	return text
}

func quoted(text string) string {
	if text == "" {
		return ""
	}
	return "+ " + strconv.Quote(text)
}
