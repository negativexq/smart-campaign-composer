// Package segment is the trust boundary for LLM audience proposals: untrusted
// JSON becomes a validated Spec, and only a Spec can become SQL.
package segment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxWindowDays = 365
	// A quote this short or long says nothing about why a filter was chosen.
	minEvidenceQuote = 3
	maxEvidenceQuote = 200
)

// languageNames lists the languages present in user data.
var languageNames = map[string]string{"tr": "Turkish", "en": "English"}

// Spec is the allowlist of audience filters. Nil fields are not applied.
type Spec struct {
	City                   *string `json:"city,omitempty"`
	Language               *string `json:"language,omitempty"`
	CartAddedWithinDays    *int    `json:"cart_added_within_days,omitempty"`
	NoPurchaseSinceCart    *bool   `json:"no_purchase_since_cart,omitempty"`
	PurchasedWithinDays    *int    `json:"purchased_within_days,omitempty"`
	NotPurchasedWithinDays *int    `json:"not_purchased_within_days,omitempty"`
	NotMessagedWithinDays  *int    `json:"not_messaged_within_days,omitempty"`
}

// IsEmpty reports whether the spec applies no filter at all.
func (s Spec) IsEmpty() bool {
	return s.City == nil && s.Language == nil && s.CartAddedWithinDays == nil &&
		!s.noPurchaseSinceCart() && s.PurchasedWithinDays == nil && s.NotPurchasedWithinDays == nil && s.NotMessagedWithinDays == nil
}

func (s Spec) noPurchaseSinceCart() bool {
	return s.NoPurchaseSinceCart != nil && *s.NoPurchaseSinceCart
}

// Assumption records a value the model chose that the brief did not state.
type Assumption struct {
	Field string `json:"field"`
	Note  string `json:"note"`
}

// Evidence is the wording a filter came from. Only KeepQuotedEvidence makes it
// trustworthy, since a model can claim anything.
type Evidence struct {
	Field string `json:"field"`
	Quote string `json:"quote"`
}

// Proposal is a validated audience proposal. UnsupportedConditions lists parts
// of the brief no filter can express; they block approval until resolved.
// UpdatedBrief is the brief rewritten for a refinement. It moves to the campaign
// once checked, so it is never stored with the proposal.
type Proposal struct {
	CampaignName          string       `json:"campaign_name,omitempty"`
	UpdatedBrief          string       `json:"updated_brief,omitempty"`
	Segment               Spec         `json:"segment"`
	Assumptions           []Assumption `json:"assumptions"`
	Evidence              []Evidence   `json:"evidence"`
	UnsupportedConditions []string     `json:"unsupported_conditions"`
	// Unmatched is what the brief mentions and the rule leaves out. Only
	// CheckAgainstBrief writes it, so a model cannot clear it.
	Unmatched []string `json:"unmatched,omitempty"`
}

// ValidationError lists everything wrong with a proposal, in words that work
// both as repair instructions for the model and as a message for the marketer.
// Reasons has one fixed code per problem; problems can quote model output, so
// only reasons are safe as metric labels.
type ValidationError struct {
	Problems []string
	Reasons  []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}

func (e *ValidationError) add(reason, problem string) {
	e.Reasons = append(e.Reasons, reason)
	e.Problems = append(e.Problems, problem)
}

func invalid(reason, problem string) *ValidationError {
	e := &ValidationError{}
	e.add(reason, problem)
	return e
}

// ParseProposal decodes and validates untrusted model output. City spellings
// are resolved to the canonical entry in cities.
func ParseProposal(raw []byte, cities []string) (Proposal, error) {
	var decoded struct {
		CampaignName          string       `json:"campaign_name"`
		UpdatedBrief          string       `json:"updated_brief"`
		Segment               *Spec        `json:"segment"`
		Assumptions           []Assumption `json:"assumptions"`
		Evidence              []Evidence   `json:"evidence"`
		UnsupportedConditions []string     `json:"unsupported_conditions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Proposal{}, invalid("malformed_output", "output is not valid JSON for the schema: "+err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Proposal{}, invalid("malformed_output", "output is not valid JSON for the schema: trailing content after the object")
	}
	if decoded.Segment == nil {
		return Proposal{}, invalid("missing_segment", "segment is required")
	}

	proposal := Proposal{
		CampaignName:          decoded.CampaignName,
		UpdatedBrief:          decoded.UpdatedBrief,
		Segment:               *decoded.Segment,
		Assumptions:           decoded.Assumptions,
		Evidence:              decoded.Evidence,
		UnsupportedConditions: cleanConditions(decoded.UnsupportedConditions),
	}
	if proposal.Assumptions == nil {
		proposal.Assumptions = []Assumption{}
	}
	if proposal.Evidence == nil {
		proposal.Evidence = []Evidence{}
	}
	if problems := proposal.validate(cities); len(problems.Problems) > 0 {
		return Proposal{}, problems
	}
	return proposal, nil
}

func cleanConditions(conditions []string) []string {
	cleaned := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		if condition = strings.TrimSpace(condition); condition != "" {
			cleaned = append(cleaned, condition)
		}
	}
	return cleaned
}

func (p *Proposal) validate(cities []string) *ValidationError {
	problems := &ValidationError{}
	spec := &p.Segment

	if strings.TrimSpace(p.CampaignName) == "" {
		p.CampaignName = ""
	} else if name, ok := CleanCampaignName(p.CampaignName); ok {
		p.CampaignName = name
	} else {
		problems.add("invalid_campaign_name", fmt.Sprintf("campaign_name must be plain text of at most %d characters", MaxCampaignNameLength))
	}

	if spec.City != nil {
		canonical, ok := resolveCity(*spec.City, cities)
		if !ok {
			problems.add("unknown_city", fmt.Sprintf("city %q is not available; use one of: %s", *spec.City, strings.Join(cities, ", ")))
		} else {
			spec.City = &canonical
		}
	}
	if spec.Language != nil {
		if _, ok := languageNames[*spec.Language]; !ok {
			problems.add("unknown_language", fmt.Sprintf("language %q is not available; use tr or en", *spec.Language))
		}
	}
	for _, window := range []struct {
		field string
		days  *int
	}{
		{"cart_added_within_days", spec.CartAddedWithinDays},
		{"purchased_within_days", spec.PurchasedWithinDays},
		{"not_purchased_within_days", spec.NotPurchasedWithinDays},
		{"not_messaged_within_days", spec.NotMessagedWithinDays},
	} {
		if window.days != nil && (*window.days < 1 || *window.days > maxWindowDays) {
			problems.add("window_out_of_range", fmt.Sprintf("%s must be between 1 and %d", window.field, maxWindowDays))
		}
	}
	if spec.noPurchaseSinceCart() && spec.CartAddedWithinDays == nil {
		problems.add("cart_window_required", "no_purchase_since_cart requires cart_added_within_days")
	}
	if spec.PurchasedWithinDays != nil && spec.NotPurchasedWithinDays != nil &&
		*spec.NotPurchasedWithinDays >= *spec.PurchasedWithinDays {
		problems.add("contradictory_windows", "purchased_within_days and not_purchased_within_days contradict each other")
	}
	if spec.IsEmpty() && len(p.UnsupportedConditions) == 0 {
		problems.add("empty_segment", "segment must have at least one filter")
	}
	for _, assumption := range p.Assumptions {
		if !slices.Contains(filterFields, assumption.Field) {
			problems.add("unknown_assumption_field", fmt.Sprintf("assumption refers to unknown field %q", assumption.Field))
		}
	}
	for _, evidence := range p.Evidence {
		if !slices.Contains(filterFields, evidence.Field) {
			problems.add("unknown_evidence_field", fmt.Sprintf("evidence refers to unknown field %q", evidence.Field))
		}
	}
	return problems
}

// KeepQuotedEvidence removes evidence for filters that are not applied and quotes
// that do not appear in sources, and returns how many it removed. A wrong quote
// costs only the explanation, so it does not reject the proposal.
func (p *Proposal) KeepQuotedEvidence(sources ...string) int {
	applied := Describe(p.Segment)
	kept := make([]Evidence, 0, len(p.Evidence))
	for _, evidence := range p.Evidence {
		isApplied := slices.ContainsFunc(applied, func(condition Condition) bool { return condition.Field == evidence.Field })
		if isApplied && quoted(evidence.Quote, sources) {
			kept = append(kept, evidence)
		}
	}
	dropped := len(p.Evidence) - len(kept)
	p.Evidence = kept
	return dropped
}

func quoted(quote string, sources []string) bool {
	folded := foldWords(quote)
	if length := utf8.RuneCountInString(folded); length < minEvidenceQuote || length > maxEvidenceQuote {
		return false
	}
	return slices.ContainsFunc(sources, func(source string) bool { return strings.Contains(foldWords(source), folded) })
}

// foldWords ignores what a model may change while copying: case and spacing.
func foldWords(text string) string {
	return strings.Join(strings.Fields(foldCity(text)), " ")
}

var filterFields = []string{
	"city", "language", "cart_added_within_days", "no_purchase_since_cart",
	"purchased_within_days", "not_purchased_within_days", "not_messaged_within_days",
}

var cityFolder = strings.NewReplacer("İ", "i", "I", "i", "ı", "i")

// foldCity makes İstanbul, Istanbul and ISTANBUL compare equal.
func foldCity(city string) string {
	return strings.ToLower(cityFolder.Replace(strings.TrimSpace(city)))
}

func resolveCity(city string, cities []string) (string, bool) {
	for _, candidate := range cities {
		if foldCity(candidate) == foldCity(city) {
			return candidate, true
		}
	}
	return "", false
}

// Condition is one filter in plain language, shown before any count.
type Condition struct {
	Field string `json:"field"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// Describe explains a spec in the marketer's words.
func Describe(spec Spec) []Condition {
	conditions := []Condition{}
	if spec.City != nil {
		conditions = append(conditions, Condition{"city", "City is", *spec.City})
	}
	if spec.Language != nil {
		conditions = append(conditions, Condition{"language", "Language is", languageNames[*spec.Language]})
	}
	if spec.CartAddedWithinDays != nil {
		conditions = append(conditions, Condition{"cart_added_within_days", "Added to cart in the last", days(*spec.CartAddedWithinDays)})
	}
	if spec.noPurchaseSinceCart() {
		conditions = append(conditions, Condition{"no_purchase_since_cart", "Purchased after adding to cart", "No"})
	}
	if spec.PurchasedWithinDays != nil {
		conditions = append(conditions, Condition{"purchased_within_days", "Purchased in the last", days(*spec.PurchasedWithinDays)})
	}
	if spec.NotPurchasedWithinDays != nil {
		conditions = append(conditions, Condition{"not_purchased_within_days", "No purchase in the last", days(*spec.NotPurchasedWithinDays)})
	}
	if spec.NotMessagedWithinDays != nil {
		conditions = append(conditions, Condition{"not_messaged_within_days", "No message from us in the last", days(*spec.NotMessagedWithinDays)})
	}
	return conditions
}

func days(count int) string {
	if count == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", count)
}

// JSONSchema is the output format requested from LLM providers. It only shapes
// the output; ParseProposal stays the authority, since numeric ranges and the
// city list cannot be enforced here.
func JSONSchema() map[string]any {
	integer := func(description string) map[string]any {
		return map[string]any{"type": "integer", "description": description}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"campaign_name", "updated_brief", "segment", "assumptions", "evidence", "unsupported_conditions"},
		"properties": map[string]any{
			"campaign_name": map[string]any{"type": "string", "description": "Short title of 2 to 5 words, at most 80 characters"},
			"updated_brief": map[string]any{"type": "string", "description": "The whole brief with the refinement applied; empty when there is no refinement"},
			"segment": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"city":                      map[string]any{"type": "string", "description": "One of the available cities"},
					"language":                  map[string]any{"type": "string", "enum": []string{"tr", "en"}},
					"cart_added_within_days":    integer("Added a product to the cart within this many days, 1 to 365"),
					"no_purchase_since_cart":    map[string]any{"type": "boolean", "description": "Did not purchase after that cart event; requires cart_added_within_days"},
					"purchased_within_days":     integer("Purchased within this many days, 1 to 365"),
					"not_purchased_within_days": integer("Did not purchase within this many days, 1 to 365"),
					"not_messaged_within_days":  integer("Received no campaign message within this many days, 1 to 365"),
				},
			},
			"assumptions": map[string]any{
				"type":        "array",
				"description": "Every value the brief did not state explicitly",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"field", "note"},
					"properties": map[string]any{
						"field": map[string]any{"type": "string", "enum": filterFields},
						"note":  map[string]any{"type": "string"},
					},
				},
			},
			"evidence": map[string]any{
				"type":        "array",
				"description": "For each filter, the exact words of the brief or refinement it came from",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"field", "quote"},
					"properties": map[string]any{
						"field": map[string]any{"type": "string", "enum": filterFields},
						"quote": map[string]any{"type": "string", "description": "Copied character for character, never translated"},
					},
				},
			},
			"unsupported_conditions": map[string]any{
				"type":        "array",
				"description": "Parts of the brief that no filter can express, in the brief's own words",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}
