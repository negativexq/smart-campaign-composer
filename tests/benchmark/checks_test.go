package benchmark

import (
	"fmt"
	"sort"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

func proposalFor(spec segment.Spec, c Case) segment.Proposal {
	proposal := segment.Proposal{Segment: spec, Assumptions: []segment.Assumption{}, Evidence: []segment.Evidence{}, UnsupportedConditions: []string{}, NotAudience: c.NotAudience}
	for _, field := range c.Assumed {
		proposal.Assumptions = append(proposal.Assumptions, segment.Assumption{Field: field, Note: "assumed"})
	}
	return proposal
}

// caught says whether the backend stops this answer from being sent: it rejects it,
// holds it for a person, or the rule is empty and matches nobody.
func caught(spec segment.Spec, c Case, brief string) bool {
	proposal := proposalFor(spec, c)
	problems := segment.CheckAgainstBrief(&proposal, brief, seed.Cities, nil)
	return problems != nil || len(proposal.Unmatched) > 0 || spec.IsEmpty()
}

type tally struct {
	total int
	gaps  []string
}

func (t *tally) check(ok bool, id, detail string) {
	t.total++
	if !ok {
		t.gaps = append(t.gaps, id+" "+detail)
	}
}

// wrongAnswers are the mistakes a model could make on a correct rule.
func wrongAnswers(rule Rule) map[string]segment.Spec {
	wrong := map[string]segment.Spec{}
	spec := rule.Spec()
	if spec.City != nil {
		other := seed.Cities[0]
		if other == *spec.City {
			other = seed.Cities[1]
		}
		changed := spec
		changed.City = &other
		wrong["other_city"] = changed
		changed = spec
		changed.City = nil
		wrong["dropped_city"] = changed
	}
	for name, field := range map[string]func(*segment.Spec) **int{
		"cart":          func(s *segment.Spec) **int { return &s.CartAddedWithinDays },
		"purchased":     func(s *segment.Spec) **int { return &s.PurchasedWithinDays },
		"not_purchased": func(s *segment.Spec) **int { return &s.NotPurchasedWithinDays },
		"not_messaged":  func(s *segment.Spec) **int { return &s.NotMessagedWithinDays },
	} {
		days := *field(&spec)
		if days == nil {
			continue
		}
		longer := *days*3 + 1
		changed := spec
		*field(&changed) = &longer
		wrong["other_"+name+"_window"] = changed
		changed = spec
		*field(&changed) = nil
		wrong["dropped_"+name+"_window"] = changed
	}
	if spec.PurchasedWithinDays != nil && spec.NotPurchasedWithinDays == nil {
		changed := spec
		changed.PurchasedWithinDays, changed.NotPurchasedWithinDays = nil, spec.PurchasedWithinDays
		wrong["flipped_to_not_purchased"] = changed
	}
	if spec.NotPurchasedWithinDays != nil && spec.PurchasedWithinDays == nil {
		changed := spec
		changed.NotPurchasedWithinDays, changed.PurchasedWithinDays = nil, spec.NotPurchasedWithinDays
		wrong["flipped_to_purchased"] = changed
	}
	return wrong
}

// TestChecksOnTheCases measures the backend with no model involved. A correct answer
// must pass, and a wrong one must be stopped.
func TestChecksOnTheCases(t *testing.T) {
	cases, err := Load("cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 150 {
		t.Fatalf("only %d cases", len(cases))
	}
	seen := map[string]bool{}
	correct, wrong, naive := &tally{}, &tally{}, &tally{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Fatalf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		if c.Kind == "content" {
			continue
		}
		brief := c.Brief
		if c.Kind == "refine" {
			brief = c.UpdatedBrief
		}
		// A safe case may end blocked, so its correct answer proves nothing here.
		if c.Rule != nil && brief != "" && c.Expect == ExpectPass {
			correct.check(!caught(c.Rule.Spec(), c, brief), c.ID, fmt.Sprintf("%q", brief))
			if !c.Rule.Spec().IsEmpty() {
				for name, spec := range wrongAnswers(*c.Rule) {
					wrong.check(caught(spec, c, brief), c.ID+" "+name, fmt.Sprintf("%q", brief))
				}
			}
		}
		if c.Naive != nil {
			naive.check(caught(c.Naive.Spec(), c, brief), c.ID, fmt.Sprintf("%q", brief))
		}
	}
	report := func(title string, result *tally) {
		sort.Strings(result.gaps)
		t.Logf("%s: %d of %d as expected", title, result.total-len(result.gaps), result.total)
		for _, gap := range result.gaps {
			t.Logf("  %s", gap)
		}
	}
	report("correct answers that pass", correct)
	report("wrong answers that are stopped", wrong)
	report("answers that drop a condition and are stopped", naive)
}
