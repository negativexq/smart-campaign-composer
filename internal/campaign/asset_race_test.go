package campaign_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// sharedService works on the test's full pool, so two calls can run at once.
func (h *harness) sharedService() *campaign.Service {
	return campaign.NewService(h.pool, h.provider, h.metrics, noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
}

// newImage records a picture of its own size, so it is not taken for another one.
func (h *harness) newImage(t *testing.T, width int) campaign.Asset {
	t.Helper()
	raw := new(bytes.Buffer)
	if err := png.Encode(raw, image.NewRGBA(image.Rect(0, 0, width, 2))); err != nil {
		t.Fatal(err)
	}
	picture, err := asset.Process(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := h.service.RecordAsset(context.Background(), "picture.png", picture)
	if err != nil {
		t.Fatal(err)
	}
	return recorded
}

func designShowing(name, assetID string) content.Design {
	return content.Design{Name: name, Accent: "#0A7A5C", Page: "#F5F7FA", Button: "pill", Corners: 8, Font: "sans", Blocks: []content.Block{
		{ID: "a", Type: "image", AssetID: assetID, Alt: "Hero", Width: 100},
		{ID: "b", Type: "heading", Slot: content.SlotHeadline},
		{ID: "c", Type: "text", Slot: content.SlotBody},
		{ID: "d", Type: "button", Slot: content.SlotCTA},
	}}
}

// holdingSession opens a transaction for the test to hold locks in, and gives its pid.
func (h *harness) holdingSession(t *testing.T) (pgx.Tx, int32) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return tx, pid
}

// waiterOn waits until exactly one session of this database is blocked by pid alone,
// and gives that session's pid.
func (h *harness) waiterOn(t *testing.T, pid int32) int32 {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiters []int32
		if err := h.pool.QueryRow(context.Background(), `
			SELECT COALESCE(array_agg(pid), '{}') FROM pg_stat_activity
			WHERE datname = current_database() AND pg_blocking_pids(pid) = ARRAY[$1::int]`, pid).Scan(&waiters); err != nil {
			t.Fatal(err)
		}
		if len(waiters) == 1 {
			return waiters[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no single session waited on %d: %v", pid, waiters)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func result[T any](t *testing.T, done <-chan T) T {
	t.Helper()
	select {
	case value := <-done:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("the call did not finish")
		var zero T
		return zero
	}
}

// A delete that comes while a save holds the image waits for it, then finds the image
// in use. The save is held at its insert, after it has locked the image, by a design
// of the same name the test has not committed.
func TestADeleteWaitsForASaveThatShowsTheImage(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	service := h.sharedService()
	hero := h.newImage(t, 4)
	hold, holder := h.holdingSession(t)
	if _, err := hold.Exec(ctx, `INSERT INTO email_designs (name, spec) VALUES ('Mine', '{}')`); err != nil {
		t.Fatal(err)
	}

	saved := make(chan error, 1)
	go func() {
		_, err := service.SaveEmailDesign(ctx, "", designShowing("Mine", hero.ID))
		saved <- err
	}()
	saving := h.waiterOn(t, holder)
	deleted := make(chan error, 1)
	go func() {
		_, err := service.DeleteAsset(ctx, hero.ID)
		deleted <- err
	}()
	h.waiterOn(t, saving)
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	if err := result(t, saved); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := result(t, deleted); !errors.Is(err, campaign.ErrAssetInUse) {
		t.Fatalf("delete: %v, want in use", err)
	}
	if _, err := service.AssetByID(ctx, hero.ID); err != nil {
		t.Fatalf("the image: %v", err)
	}
}

// A save that comes while a delete holds the image waits for it, then finds the image
// gone and keeps nothing. The test stands in for the delete.
func TestASaveRefusesAnImageDeletedMeanwhile(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	service := h.sharedService()
	hero := h.newImage(t, 5)
	hold, holder := h.holdingSession(t)
	if _, err := hold.Exec(ctx, `SELECT 1 FROM assets WHERE id = $1 FOR UPDATE`, hero.ID); err != nil {
		t.Fatal(err)
	}

	saved := make(chan error, 1)
	go func() {
		_, err := service.SaveEmailDesign(ctx, "", designShowing("Mine", hero.ID))
		saved <- err
	}()
	h.waiterOn(t, holder)
	if _, err := hold.Exec(ctx, `DELETE FROM assets WHERE id = $1`, hero.ID); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var gone *campaign.DesignError
	if err := result(t, saved); !errors.As(err, &gone) {
		t.Fatalf("save: %v, want the image gone", err)
	}
	if designs, err := service.EmailDesigns(ctx); err != nil || len(designs) != 0 {
		t.Fatalf("designs: %+v %v", designs, err)
	}
}

// A campaign takes its copy of a design before it is written, so an image removed
// from the library in between refuses the campaign rather than leave it broken.
func TestACampaignRefusesADesignWhoseImageWasDeletedMeanwhile(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	service := h.sharedService()
	hero := h.newImage(t, 6)
	design, err := service.SaveEmailDesign(ctx, "", designShowing("Mine", hero.ID))
	if err != nil {
		t.Fatal(err)
	}
	hold, holder := h.holdingSession(t)
	if _, err := hold.Exec(ctx, `SELECT 1 FROM assets WHERE id = $1 FOR UPDATE`, hero.ID); err != nil {
		t.Fatal(err)
	}

	created := make(chan error, 1)
	go func() {
		_, err := service.CreateWith(ctx, campaign.NewCampaign{Brief: "Istanbul cart reminder", Channels: []string{"email"}, EmailTemplate: content.CustomTemplateName(design.ID)})
		created <- err
	}()
	h.waiterOn(t, holder)
	// The design is changed and its image deleted while the campaign waits.
	if _, err := hold.Exec(ctx, `DELETE FROM email_designs WHERE id = $1`, design.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `DELETE FROM assets WHERE id = $1`, hero.ID); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var gone *campaign.DesignError
	if err := result(t, created); !errors.As(err, &gone) {
		t.Fatalf("create: %v, want the image gone", err)
	}
}

// What a campaign's messages show keeps their images, even when only the version
// remembers the design: the campaign's own copy and the library's are both gone.
func TestAnImageOnlyAVersionShowsStays(t *testing.T) {
	t.Parallel()
	h := newHarness(t, step{output: validProposal})
	ctx := context.Background()
	hero := h.newImage(t, 7)
	design, err := h.service.SaveEmailDesign(ctx, "", designShowing("Mine", hero.ID))
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.service.CreateWith(ctx, campaign.NewCampaign{Brief: secretBrief, Channels: []string{"email"}, EmailTemplate: content.CustomTemplateName(design.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	// As generating leaves it: the version carries the campaign's copy of the design.
	if _, err := h.pool.Exec(ctx, `
		UPDATE campaign_versions v SET content = jsonb_build_object('email_design', c.email_design)
		FROM campaigns c WHERE c.id = v.campaign_id AND c.id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `UPDATE campaigns SET email_design = NULL WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := h.service.DeleteEmailDesign(ctx, design.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.service.DeleteAsset(ctx, hero.ID); !errors.Is(err, campaign.ErrAssetInUse) {
		t.Fatalf("delete an image a version shows: %v", err)
	}
}

// Generating copies the campaign's own design into its messages, so the images stay
// referenced while the model writes. Choosing another design in an edit takes a copy
// from the library, and an image deleted while the edit waits refuses it.
func TestGeneratedMessagesKeepTheirImagesAndAnEditChecksNewOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	mock := ai.NewMock()
	mock.StreamDelay = 0
	service := campaign.NewService(h.pool, mock, h.metrics, noop.NewTracerProvider().Tracer("test"), func() time.Time { return referenceTime })
	hero, other := h.newImage(t, 8), h.newImage(t, 9)
	first, err := service.SaveEmailDesign(ctx, "", designShowing("First", hero.ID))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.SaveEmailDesign(ctx, "", designShowing("Second", other.ID))
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.CreateWith(ctx, campaign.NewCampaign{
		Brief:         "Send a 30% discount reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy.",
		Channels:      []string{"email"},
		EmailTemplate: content.CustomTemplateName(first.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	generation, err := service.StartGeneration(ctx, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	if _, err := generation.Run(ctx, func(campaign.GenerationEvent) {}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	var carried bool
	if err := h.pool.QueryRow(ctx, `SELECT bool_and(content::text LIKE '%' || $2 || '%') FROM campaign_versions WHERE campaign_id = $1`, id, hero.ID).Scan(&carried); err != nil || !carried {
		t.Fatalf("the messages do not carry the design's image: %v %v", carried, err)
	}
	if err := service.DeleteEmailDesign(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeleteAsset(ctx, hero.ID); !errors.Is(err, campaign.ErrAssetInUse) {
		t.Fatalf("delete an image the messages show: %v", err)
	}

	view, err := service.Get(ctx, id)
	if err != nil || view.Content == nil {
		t.Fatalf("read: %v", err)
	}
	edited := view.Content.Content
	edited.EmailTemplate = content.CustomTemplateName(second.ID)
	hold, holder := h.holdingSession(t)
	if _, err := hold.Exec(ctx, `SELECT 1 FROM assets WHERE id = $1 FOR UPDATE`, other.ID); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		_, err := service.SaveContent(ctx, id, edited)
		saved <- err
	}()
	h.waiterOn(t, holder)
	if _, err := hold.Exec(ctx, `DELETE FROM email_designs WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `DELETE FROM assets WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var gone *campaign.DesignError
	if err := result(t, saved); !errors.As(err, &gone) {
		t.Fatalf("edit: %v, want the image gone", err)
	}
}
