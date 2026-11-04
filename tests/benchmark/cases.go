// Package benchmark holds hard briefs with the answer a careful person would give,
// and two ways to score them. checks_test.go runs in `make test` and measures the
// backend alone, with made-up model answers. live_test.go asks a real model.
package benchmark

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

// What a case expects. A "safe" case may end either way, so only a wrong rule that
// could be sent counts against it.
const (
	ExpectPass  = "pass"
	ExpectBlock = "block"
	ExpectSafe  = "safe"
)

// Window is a length of time in days. Some are ranges, because a month is.
type Window struct{ Min, Max int }

// UnmarshalJSON reads 30 or [28, 31].
func (w *Window) UnmarshalJSON(data []byte) error {
	var pair []int
	if err := json.Unmarshal(data, &pair); err == nil && len(pair) == 2 {
		w.Min, w.Max = pair[0], pair[1]
		return nil
	}
	if err := json.Unmarshal(data, &w.Min); err != nil {
		return fmt.Errorf("window: %w", err)
	}
	w.Max = w.Min
	return nil
}

func (w *Window) matches(days *int) bool {
	if w == nil || days == nil {
		return w == nil && days == nil
	}
	return *days >= w.Min && *days <= w.Max
}

func (w *Window) days() *int {
	if w == nil {
		return nil
	}
	middle := (w.Min + w.Max) / 2
	return &middle
}

// Rule is the audience a case expects.
type Rule struct {
	City         *string `json:"city"`
	Language     *string `json:"language"`
	Cart         *Window `json:"cart_added_within_days"`
	NoPurchase   bool    `json:"no_purchase_since_cart"`
	Purchased    *Window `json:"purchased_within_days"`
	NotPurchased *Window `json:"not_purchased_within_days"`
	NotMessaged  *Window `json:"not_messaged_within_days"`
}

// Matches reports whether a stored rule is this one.
func (r Rule) Matches(spec segment.Spec) bool {
	return same(r.City, spec.City) && same(r.Language, spec.Language) &&
		r.Cart.matches(spec.CartAddedWithinDays) &&
		r.NoPurchase == (spec.NoPurchaseSinceCart != nil && *spec.NoPurchaseSinceCart) &&
		r.Purchased.matches(spec.PurchasedWithinDays) &&
		r.NotPurchased.matches(spec.NotPurchasedWithinDays) &&
		r.NotMessaged.matches(spec.NotMessagedWithinDays)
}

func same(want, got *string) bool {
	if want == nil || got == nil {
		return want == nil && got == nil
	}
	return *want == *got
}

// Spec is this rule as a model would answer it.
func (r Rule) Spec() segment.Spec {
	spec := segment.Spec{
		City: r.City, Language: r.Language, CartAddedWithinDays: r.Cart.days(),
		PurchasedWithinDays: r.Purchased.days(), NotPurchasedWithinDays: r.NotPurchased.days(), NotMessagedWithinDays: r.NotMessaged.days(),
	}
	if r.NoPurchase {
		yes := true
		spec.NoPurchaseSinceCart = &yes
	}
	return spec
}

// Case is one hard brief.
type Case struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	Kind        string   `json:"kind"`
	Brief       string   `json:"brief"`
	Expect      string   `json:"expect"`
	Rule        *Rule    `json:"rule"`
	Alts        []Rule   `json:"alts"`
	Assumed     []string `json:"assumed"`
	NotAudience []string `json:"not_audience"`
	Naive       *Rule    `json:"naive"`
	Note        string   `json:"note"`

	First        *Rule  `json:"first"`
	Refinement   string `json:"refinement"`
	UpdatedBrief string `json:"updated_brief"`
	Noted        *bool  `json:"noted"`

	Instruction string   `json:"instruction"`
	Channel     string   `json:"channel"`
	Must        []string `json:"must"`
	MustNot     []string `json:"must_not"`
	Refused     bool     `json:"refused"`
}

// Load reads the cases.
func Load(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cases: %w", err)
	}
	var file struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode cases: %w", err)
	}
	return file.Cases, nil
}
