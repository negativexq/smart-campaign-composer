package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

func (h *harness) createWithStatus(t *testing.T, brief, status string) string {
	t.Helper()
	ctx := context.Background()
	id, err := h.service.Create(ctx, brief)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatalf("set status: %v", err)
	}
	return id
}

func briefsOf(summaries []campaign.Summary) map[string]bool {
	briefs := map[string]bool{}
	for _, summary := range summaries {
		briefs[summary.Brief] = true
	}
	return briefs
}

func TestListFiltersByGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	for brief, status := range map[string]string{
		"draft":     "DRAFT",
		"review":    "READY_FOR_REVIEW",
		"sending":   "DISPATCHING",
		"done":      "COMPLETED",
		"partly":    "COMPLETED_WITH_FAILURES",
		"cancelled": "CANCELLED",
	} {
		h.createWithStatus(t, brief, status)
	}
	tests := []struct {
		group string
		want  []string
	}{
		{"", []string{"draft", "review", "sending", "done", "partly", "cancelled"}},
		{"drafts", []string{"draft", "review"}},
		{"active", []string{"sending"}},
		{"completed", []string{"done"}},
		{"failed", []string{"partly"}},
	}
	for _, tt := range tests {
		got, err := h.service.List(context.Background(), campaign.ListFilter{Group: tt.group})
		if err != nil {
			t.Fatalf("%q: %v", tt.group, err)
		}
		briefs := briefsOf(got)
		if len(got) != len(tt.want) {
			t.Errorf("%q: got %v", tt.group, briefs)
		}
		for _, want := range tt.want {
			if !briefs[want] {
				t.Errorf("%q: missing %s in %v", tt.group, want, briefs)
			}
		}
	}

	if _, err := h.service.List(context.Background(), campaign.ListFilter{Group: "nope"}); !errors.Is(err, campaign.ErrUnknownGroup) {
		t.Fatalf("unknown group: %v", err)
	}
}

func TestListSearchTreatsWildcardsAsText(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	h.createWithStatus(t, "Winter sale 30% off", "DRAFT")
	h.createWithStatus(t, "Winter newsletter", "DRAFT")

	for query, want := range map[string]int{"winter": 2, "30%": 1, "%": 1, "_": 0, "'; DROP TABLE campaigns; --": 0} {
		got, err := h.service.List(context.Background(), campaign.ListFilter{Query: query})
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		if len(got) != want {
			t.Errorf("%q: got %d, want %d", query, len(got), want)
		}
	}
}

func TestListProgressComesFromDeliveries(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	id := h.create(t)
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := h.pool.Exec(ctx, `
		INSERT INTO campaign_deliveries (campaign_id, campaign_version_id, user_id, channel, idempotency_key, status)
		SELECT $1::uuid, v.id, s.user_id, s.channel, $1::uuid::text || s.user_id || s.channel, s.status
		FROM campaign_versions v,
		     (VALUES (1::bigint, 'sms', 'SENT'), (1, 'email', 'FAILED'), (3, 'sms', 'QUEUED')) AS s(user_id, channel, status)
		WHERE v.campaign_id = $1::uuid`, id); err != nil {
		t.Fatalf("insert deliveries: %v", err)
	}

	got, err := h.service.List(ctx, campaign.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Total != 3 || got[0].Processed != 2 || len(got[0].Channels) != 2 {
		t.Fatalf("summary: %+v", got)
	}
}

func TestEmptyWorkspaceEncodesAsEmptyLists(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	overview, err := h.service.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	encoded, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Recent   []any `json:"recent_campaigns"`
		Activity []any `json:"recent_activity"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Recent == nil || decoded.Activity == nil {
		t.Fatalf("lists must be [] rather than null: %s", encoded)
	}
}

func TestOverviewCountsMatchTheFilters(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	for _, status := range []string{"DRAFT", "DRAFT", "DISPATCHING", "COMPLETED", "COMPLETED_WITH_FAILURES", "CANCELLED"} {
		h.createWithStatus(t, "brief "+status, status)
	}

	overview, err := h.service.Overview(context.Background())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	want := campaign.Counts{Total: 6, Drafts: 2, Active: 1, Completed: 1, Failed: 1}
	if overview.Counts != want {
		t.Fatalf("got %+v, want %+v", overview.Counts, want)
	}
	if len(overview.Recent) != 5 {
		t.Fatalf("recent: %d", len(overview.Recent))
	}
	if len(overview.Activity) != 6 || overview.Activity[0].Name == "" {
		t.Fatalf("activity: %+v", overview.Activity)
	}
}

func TestActivityTellsHowTheAudienceChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal}, step{output: withUpdatedBrief(validProposal, secretBrief)})
	ctx := context.Background()
	id := h.create(t)
	edited := "Istanbul users"
	for _, input := range []campaign.ProposeInput{{}, {Refinement: "narrower"}, {Brief: &edited}} {
		if _, err := h.service.ProposeSegment(ctx, id, input); err != nil {
			t.Fatalf("propose: %v", err)
		}
	}

	entries, err := h.service.Activity(ctx, id)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(entries) != 4 || entries[0].Type != "campaign.created" {
		t.Fatalf("entries: %+v", entries)
	}
	for index, want := range map[int]string{1: "brief", 2: "refinement", 3: "brief_edit"} {
		var details struct {
			Trigger string `json:"trigger"`
		}
		if err := json.Unmarshal(entries[index].Details, &details); err != nil {
			t.Fatal(err)
		}
		if details.Trigger != want {
			t.Errorf("entry %d: trigger %q, want %q", index, details.Trigger, want)
		}
	}

	for _, unknown := range []string{"not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
		if _, err := h.service.Activity(ctx, unknown); !errors.Is(err, campaign.ErrNotFound) {
			t.Errorf("%s: %v", unknown, err)
		}
	}
}

func TestListFiltersByChannelAndAgeAndSorts(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	for _, row := range []struct {
		brief    string
		channels []string
		age      string
	}{
		{"Beta recent email", []string{"email"}, "1 day"},
		{"Alpha both", []string{"sms", "email"}, "10 days"},
		{"Carrot old sms", []string{"sms"}, "40 days"},
	} {
		id := h.createWithStatus(t, row.brief, "DRAFT")
		if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET channels = $2, name = $3, created_at = now() - $4::interval WHERE id = $1`, id, row.channels, row.brief, row.age); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		filter campaign.ListFilter
		want   []string
	}{
		{"newest first by default", campaign.ListFilter{}, []string{"Beta recent email", "Alpha both", "Carrot old sms"}},
		{"oldest first", campaign.ListFilter{Sort: "oldest"}, []string{"Carrot old sms", "Alpha both", "Beta recent email"}},
		{"by name", campaign.ListFilter{Sort: "name"}, []string{"Alpha both", "Beta recent email", "Carrot old sms"}},
		{"uses sms, alone or not", campaign.ListFilter{Channel: "sms"}, []string{"Alpha both", "Carrot old sms"}},
		{"uses email, alone or not", campaign.ListFilter{Channel: "email"}, []string{"Beta recent email", "Alpha both"}},
		{"created recently", campaign.ListFilter{CreatedWithinDays: 30}, []string{"Beta recent email", "Alpha both"}},
		{"everything together", campaign.ListFilter{Channel: "sms", CreatedWithinDays: 30, Sort: "name"}, []string{"Alpha both"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := h.service.List(ctx, tt.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			names := []string{}
			for _, summary := range got {
				names = append(names, summary.Name)
			}
			if !slices.Equal(names, tt.want) {
				t.Fatalf("got %v, want %v", names, tt.want)
			}
		})
	}
	for _, bad := range []campaign.ListFilter{{Channel: "fax"}, {Sort: "name; DROP TABLE campaigns"}, {CreatedWithinDays: -1}} {
		if _, err := h.service.List(ctx, bad); !errors.Is(err, campaign.ErrUnknownFilter) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
