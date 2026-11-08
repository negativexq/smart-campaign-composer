package campaign_test

import (
	"context"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// The list's preview comes from columns PostgreSQL fills when a version is written,
// so an edit shows at once and a long message is cut by characters, not bytes.
func TestTheListPreviewFollowsEveryEditOfTheMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, id := readyCampaign(t)
	previewOf := func() *campaign.Preview {
		t.Helper()
		listed, err := h.service.List(ctx, campaign.ListFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range listed {
			if row.ID == id {
				return row.Preview
			}
		}
		t.Fatalf("campaign %s is not listed", id)
		return nil
	}

	view, err := h.service.Get(ctx, id)
	if err != nil || view.Content == nil {
		t.Fatalf("read: %v", err)
	}
	if got := previewOf(); got == nil || got.SMS != view.Content.Content.SMS || got.EmailSubject != view.Content.Content.EmailSubject {
		t.Fatalf("preview %+v, want the generated messages", got)
	}

	edited := view.Content.Content
	edited.SMS = "Hi {{first_name}}, your cart is still here."
	edited.EmailSubject = "Still thinking it over?"
	if _, err := h.service.SaveContent(ctx, id, edited); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := previewOf(); got == nil || got.SMS != edited.SMS || got.EmailSubject != edited.EmailSubject {
		t.Fatalf("preview %+v after an edit, want %q / %q", got, edited.SMS, edited.EmailSubject)
	}

	// Longer than any message may be, to show where the cut falls: 200 two-byte letters.
	long := strings.Repeat("ş", 200)
	if _, err := h.pool.Exec(ctx, `
		UPDATE campaign_versions SET content = jsonb_set(content, '{email_subject}', to_jsonb($2::text))
		WHERE campaign_id = $1`, id, long); err != nil {
		t.Fatal(err)
	}
	if got := previewOf(); got == nil || got.EmailSubject != strings.Repeat("ş", 160) {
		t.Fatalf("a long subject gave %d characters, want 160", len([]rune(got.EmailSubject)))
	}
}
