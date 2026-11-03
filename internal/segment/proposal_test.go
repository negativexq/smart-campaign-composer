package segment_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

func parse(raw string) (segment.Proposal, error) {
	return segment.ParseProposal([]byte(raw), seed.Cities)
}

func requireInvalid(t *testing.T, raw, wantInMessage string) {
	t.Helper()
	_, err := parse(raw)
	var invalid *segment.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("want a ValidationError, got: %v", err)
	}
	if !strings.Contains(invalid.Error(), wantInMessage) {
		t.Fatalf("message %q does not mention %q", invalid.Error(), wantInMessage)
	}
}

func TestParseValidProposal(t *testing.T) {
	t.Parallel()
	proposal, err := parse(`{
		"segment": {"city": "İstanbul", "cart_added_within_days": 30, "no_purchase_since_cart": true},
		"assumptions": [{"field": "cart_added_within_days", "note": "Used the window from the brief"}],
		"unsupported_conditions": []
	}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *proposal.Segment.City != "İstanbul" || *proposal.Segment.CartAddedWithinDays != 30 {
		t.Fatalf("unexpected segment: %+v", proposal.Segment)
	}
	if len(proposal.Assumptions) != 1 {
		t.Fatalf("assumptions lost: %+v", proposal.Assumptions)
	}
}

func TestParseNormalizesCitySpelling(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"Istanbul", "istanbul", "İSTANBUL", "ISTANBUL", " İstanbul "} {
		proposal, err := parse(`{"segment": {"city": "` + spelling + `"}, "assumptions": [], "unsupported_conditions": []}`)
		if err != nil {
			t.Fatalf("%q: %v", spelling, err)
		}
		if *proposal.Segment.City != seed.CityIstanbul {
			t.Errorf("%q became %q", spelling, *proposal.Segment.City)
		}
	}
}

func TestParseRejectsUntrustedOutput(t *testing.T) {
	t.Parallel()
	const tail = `, "assumptions": [], "unsupported_conditions": []}`
	tests := []struct{ name, raw, message string }{
		{"not json", `here is your segment`, "valid JSON"},
		{"truncated json", `{"segment": {"city": "Ank`, "valid JSON"},
		{"trailing content", `{"segment": {"city": "Ankara"}` + tail + ` extra`, "valid JSON"},
		{"missing segment", `{"assumptions": [], "unsupported_conditions": []}`, "segment"},
		{"unknown filter", `{"segment": {"age_over": 25}` + tail, "age_over"},
		{"raw sql field", `{"segment": {"where": "1=1; DROP TABLE users"}` + tail, "where"},
		{"unknown top-level field", `{"segment": {"city": "Ankara"}, "sql": "SELECT 1"` + tail, "sql"},
		{"sql in city", `{"segment": {"city": "Ankara' OR '1'='1"}` + tail, "city"},
		{"unknown city", `{"segment": {"city": "Atlantis"}` + tail, "Atlantis"},
		{"unknown language", `{"segment": {"city": "Ankara", "language": "de"}` + tail, "language"},
		{"zero days", `{"segment": {"cart_added_within_days": 0}` + tail, "cart_added_within_days"},
		{"negative days", `{"segment": {"purchased_within_days": -5}` + tail, "purchased_within_days"},
		{"too many days", `{"segment": {"not_purchased_within_days": 366}` + tail, "not_purchased_within_days"},
		{"float days", `{"segment": {"cart_added_within_days": 1.5}` + tail, "valid JSON"},
		{"no purchase without cart window", `{"segment": {"no_purchase_since_cart": true}` + tail, "cart_added_within_days"},
		{"contradictory purchase windows", `{"segment": {"purchased_within_days": 30, "not_purchased_within_days": 60}` + tail, "contradict"},
		{"everyone", `{"segment": {}` + tail, "at least one"},
		{"unknown assumption field", `{"segment": {"city": "Ankara"}, "assumptions": [{"field": "age", "note": "x"}], "unsupported_conditions": []}`, "age"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireInvalid(t, tt.raw, tt.message)
		})
	}
}

func TestParseAllowsEmptySegmentOnlyWhenItSaysWhatItCouldNotUse(t *testing.T) {
	t.Parallel()
	proposal, err := parse(`{"segment": {}, "assumptions": [], "unsupported_conditions": ["users over 25"]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !proposal.Segment.IsEmpty() {
		t.Fatal("segment should be empty")
	}
	if len(proposal.UnsupportedConditions) != 1 {
		t.Fatalf("unsupported condition lost: %+v", proposal)
	}
}

func TestDescribeUsesPlainLanguage(t *testing.T) {
	t.Parallel()
	proposal, err := parse(`{"segment": {"city": "Ankara", "cart_added_within_days": 30, "no_purchase_since_cart": true, "language": "tr"},
		"assumptions": [], "unsupported_conditions": []}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	got := map[string]string{}
	for _, condition := range segment.Describe(proposal.Segment) {
		got[condition.Label] = condition.Value
	}

	for label, value := range map[string]string{
		"City is":                        "Ankara",
		"Added to cart in the last":      "30 days",
		"Purchased after adding to cart": "No",
		"Language is":                    "Turkish",
	} {
		if got[label] != value {
			t.Errorf("%q: got %q, want %q", label, got[label], value)
		}
	}
}

func TestSchemaForbidsExtraFields(t *testing.T) {
	t.Parallel()
	schema := segment.JSONSchema()
	if schema["additionalProperties"] != false {
		t.Fatal("top level must forbid additional properties")
	}
	properties := schema["properties"].(map[string]any)
	inner := properties["segment"].(map[string]any)
	if inner["additionalProperties"] != false {
		t.Fatal("segment must forbid additional properties")
	}
}

func TestNotMessagedWindowIsValidatedAndDescribed(t *testing.T) {
	t.Parallel()
	cities := []string{"İstanbul"}
	proposal, err := segment.ParseProposal([]byte(`{"segment":{"city":"İstanbul","not_messaged_within_days":7},"assumptions":[{"field":"not_messaged_within_days","note":"x"}],"unsupported_conditions":[]}`), cities)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conditions := segment.Describe(proposal.Segment)
	if len(conditions) != 2 || conditions[1].Field != "not_messaged_within_days" || conditions[1].Value != "7 days" {
		t.Fatalf("conditions: %+v", conditions)
	}
	for _, days := range []string{"0", "366"} {
		if _, err := segment.ParseProposal([]byte(`{"segment":{"not_messaged_within_days":`+days+`},"assumptions":[],"unsupported_conditions":[]}`), cities); err == nil {
			t.Errorf("%s days was accepted", days)
		}
	}
	schema := segment.JSONSchema()["properties"].(map[string]any)["segment"].(map[string]any)["properties"].(map[string]any)
	if _, ok := schema["not_messaged_within_days"]; !ok {
		t.Fatal("the model is not told about it")
	}
}

func TestEvidenceIsOptionalAndNamesARealFilter(t *testing.T) {
	t.Parallel()
	cities := []string{"İstanbul"}
	proposal, err := segment.ParseProposal([]byte(`{"segment":{"city":"İstanbul"}}`), cities)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Evidence == nil || len(proposal.Evidence) != 0 {
		t.Fatalf("evidence should be empty, not missing: %#v", proposal.Evidence)
	}
	_, err = segment.ParseProposal([]byte(`{"segment":{"city":"İstanbul"},"evidence":[{"field":"age","quote":"young"}]}`), cities)
	var invalid *segment.ValidationError
	if !errors.As(err, &invalid) || !slices.Contains(invalid.Reasons, "unknown_evidence_field") {
		t.Fatalf("got %v", err)
	}
}

func TestKeepQuotedEvidenceKeepsOnlyWhatWasWritten(t *testing.T) {
	t.Parallel()
	city, days := "İstanbul", 30
	proposal := segment.Proposal{
		Segment: segment.Spec{City: &city, CartAddedWithinDays: &days},
		Evidence: []segment.Evidence{
			{Field: "city", Quote: "ISTANBUL'daki   kullanıcılar"},
			{Field: "cart_added_within_days", Quote: "the last two weeks"},
			{Field: "cart_added_within_days", Quote: "son 30 gün"},
			{Field: "language", Quote: "kullanıcılar"},
			{Field: "city", Quote: "ka"},
			{Field: "city", Quote: strings.Repeat("kullanıcılar ", 30)},
		},
	}

	dropped := proposal.KeepQuotedEvidence("İstanbul'daki kullanıcılar sepete ekledi", "make it son 30 gün")

	want := []segment.Evidence{
		{Field: "city", Quote: "ISTANBUL'daki   kullanıcılar"},
		{Field: "cart_added_within_days", Quote: "son 30 gün"},
	}
	if !slices.Equal(proposal.Evidence, want) || dropped != 4 {
		t.Fatalf("kept %+v, dropped %d", proposal.Evidence, dropped)
	}
}
