package segment_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

func TestCleanCampaignName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
		ok                bool
	}{
		{"plain", "Cart Recovery — İstanbul", "Cart Recovery — İstanbul", true},
		{"trims and collapses spaces", "  Cart   Recovery ", "Cart Recovery", true},
		{"flattens line breaks", "Cart\nRecovery", "Cart Recovery", true},
		{"counts characters not bytes", strings.Repeat("İ", segment.MaxCampaignNameLength), strings.Repeat("İ", segment.MaxCampaignNameLength), true},
		{"empty", "   ", "", false},
		{"too long", strings.Repeat("x", segment.MaxCampaignNameLength+1), "", false},
		{"control character", "Cart\x00Recovery", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := segment.CleanCampaignName(tt.input)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("got %q %v, want %q %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseValidatesTheSuggestedName(t *testing.T) {
	t.Parallel()
	const rest = `"segment":{"city":"Ankara"},"assumptions":[],"unsupported_conditions":[]}`

	proposal, err := parse(`{"campaign_name":"  Ankara  Reminder ",` + rest)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if proposal.CampaignName != "Ankara Reminder" {
		t.Fatalf("name: %q", proposal.CampaignName)
	}

	missing, err := parse(`{` + rest)
	if err != nil || missing.CampaignName != "" {
		t.Fatalf("a missing name must not reject an audience: %q %v", missing.CampaignName, err)
	}

	requireInvalid(t, `{"campaign_name":"`+strings.Repeat("x", 81)+`",`+rest, "campaign_name")
}

func TestSchemaAsksForAName(t *testing.T) {
	t.Parallel()
	schema := segment.JSONSchema()
	if _, ok := schema["properties"].(map[string]any)["campaign_name"]; !ok {
		t.Fatal("schema must offer campaign_name")
	}
	required := schema["required"].([]string)
	if !strings.Contains(strings.Join(required, ","), "campaign_name") {
		t.Fatalf("required: %v", required)
	}
}
