package benchmark

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
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

// knownGapsFile lists the gaps the backend has today, one per line as
// "<measure>: <case> [<wrong answer>]". Comments start with #.
const knownGapsFile = "known_gaps.txt"

// loadKnownGaps reads the known gaps as a set.
func loadKnownGaps(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // read only
	known := map[string]bool{}
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		if line := strings.TrimSpace(lines.Text()); line != "" && !strings.HasPrefix(line, "#") {
			known[line] = true
		}
	}
	return known, lines.Err()
}

// gapKey names a gap without its brief, so rewording a brief does not move it.
func gapKey(measure, gap string) string {
	key, _, _ := strings.Cut(gap, " \"")
	return measure + ": " + key
}

// compareGaps says which gaps are new and which known ones no longer happen.
func compareGaps(known map[string]bool, found []string) (added, gone []string) {
	seen := map[string]bool{}
	for _, gap := range found {
		seen[gap] = true
		if !known[gap] {
			added = append(added, gap)
		}
	}
	for gap := range known {
		if !seen[gap] {
			gone = append(gone, gap)
		}
	}
	sort.Strings(added)
	sort.Strings(gone)
	return added, gone
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
	var found []string
	report := func(measure, title string, result *tally) {
		sort.Strings(result.gaps)
		t.Logf("%s: %d of %d as expected", title, result.total-len(result.gaps), result.total)
		for _, gap := range result.gaps {
			t.Logf("  %s", gap)
			found = append(found, gapKey(measure, gap))
		}
	}
	report("correct", "correct answers that pass", correct)
	report("wrong", "wrong answers that are stopped", wrong)
	report("naive", "answers that drop a condition and are stopped", naive)

	// The gaps above are measurements, but a change to them is a regression or a fix,
	// so it has to be seen: a new gap fails, and so does a known one that closed.
	known, err := loadKnownGaps(knownGapsFile)
	if err != nil {
		t.Fatal(err)
	}
	added, gone := compareGaps(known, found)
	for _, gap := range added {
		t.Errorf("new gap, not in %s: %s", knownGapsFile, gap)
	}
	for _, gap := range gone {
		t.Errorf("a known gap no longer happens; remove it from %s: %s", knownGapsFile, gap)
	}
}

func TestComparingGapsFindsNewAndClosedOnes(t *testing.T) {
	known := map[string]bool{"wrong: a-01 dropped_city": true, "naive: b-02": true}
	added, gone := compareGaps(known, []string{"wrong: a-01 dropped_city", "correct: c-03"})
	if len(added) != 1 || added[0] != "correct: c-03" || len(gone) != 1 || gone[0] != "naive: b-02" {
		t.Fatalf("added %v, gone %v", added, gone)
	}
	if key := gapKey("wrong", `unseen_negation-02 flipped_to_purchased "Son 45 gündür"`); key != "wrong: unseen_negation-02 flipped_to_purchased" {
		t.Fatalf("key %q", key)
	}
}
