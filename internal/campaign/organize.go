package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	maxTags       = 10
	maxTagLength  = 30
	eventTagged   = "campaign.tags_changed"
	eventArchived = "campaign.archived"
	eventRestored = "campaign.unarchived"
	eventDeleted  = "campaign.deleted"
)

// Errors the API maps to responses.
var (
	ErrInvalidTags    = errors.New("invalid tags")
	ErrArchived       = errors.New("campaign is archived")
	ErrNotArchived    = errors.New("campaign is not archived")
	ErrStillSending   = errors.New("campaign is sending")
	ErrSentBefore     = errors.New("campaign has sent")
	ErrAlreadyDeleted = errors.New("campaign is deleted")
)

// TagUse is one tag and how many campaigns in the list carry it.
type TagUse struct {
	Tag       string `json:"tag"`
	Campaigns int    `json:"campaigns"`
}

// cleanTags trims each tag, joins inner spaces and drops repeats that differ only in
// case. It refuses empty, long or too many tags rather than shortening them.
func cleanTags(tags []string) ([]string, error) {
	cleaned := []string{}
	for _, tag := range tags {
		tag = strings.Join(strings.Fields(tag), " ")
		if tag == "" || utf8.RuneCountInString(tag) > maxTagLength {
			return nil, ErrInvalidTags
		}
		if !slices.ContainsFunc(cleaned, func(known string) bool { return strings.EqualFold(known, tag) }) {
			cleaned = append(cleaned, tag)
		}
	}
	if len(cleaned) > maxTags {
		return nil, ErrInvalidTags
	}
	return cleaned, nil
}

// SetTags replaces a campaign's tags. A tag another campaign already uses keeps that
// spelling, so "sale" and "Sale" do not become two filters.
func (s *Service) SetTags(ctx context.Context, id string, tags []string) (View, error) {
	cleaned, err := cleanTags(tags)
	if err != nil {
		return View{}, err
	}
	if _, err := s.load(ctx, id); err != nil {
		return View{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin tags: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (lower(t)) t FROM campaigns c, unnest(c.tags) t
		WHERE c.id <> $1 AND c.deleted_at IS NULL AND lower(t) = ANY($2)
		ORDER BY lower(t), t`, id, lowered(cleaned))
	if err != nil {
		return View{}, fmt.Errorf("read known tags: %w", err)
	}
	known, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return View{}, fmt.Errorf("read known tags: %w", err)
	}
	for i, tag := range cleaned {
		if at := slices.IndexFunc(known, func(other string) bool { return strings.EqualFold(other, tag) }); at >= 0 {
			cleaned[i] = known[at]
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET tags = $2, updated_at = now() WHERE id = $1`, id, cleaned); err != nil {
		return View{}, fmt.Errorf("save tags: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventTagged, map[string]any{"tags": cleaned}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit tags: %w", err)
	}
	return s.Get(ctx, id)
}

func lowered(tags []string) []string {
	out := make([]string, len(tags))
	for i, tag := range tags {
		out[i] = strings.ToLower(tag)
	}
	return out
}

// Tags lists the tags on campaigns that are not archived, for filtering by.
func (s *Service) Tags(ctx context.Context) ([]TagUse, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t, count(*)::int FROM campaigns c, unnest(c.tags) t
		WHERE c.deleted_at IS NULL AND c.archived_at IS NULL
		GROUP BY t ORDER BY lower(t), t`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tags, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TagUse])
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

// lockForArchive locks a campaign that exists and says whether it is archived.
func lockForArchive(ctx context.Context, tx pgx.Tx, id string) (status string, archived bool, err error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return "", false, ErrNotFound
	}
	err = tx.QueryRow(ctx, `SELECT status, archived_at IS NOT NULL FROM campaigns WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&status, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("lock campaign: %w", err)
	}
	return status, archived, nil
}

// Archive takes a campaign out of the list and keeps everything it sent. Later sends
// of its schedule are cancelled. A campaign still writing or sending messages has to
// finish first, so nothing is left half done out of sight.
func (s *Service) Archive(ctx context.Context, id string) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin archive: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, archived, err := lockForArchive(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if archived {
		return View{}, ErrArchived
	}
	if status == statusGenerating || status == statusDispatching || status == statusPaused {
		return View{}, ErrStillSending
	}
	details := map[string]any{}
	if status == statusScheduled {
		sentBefore, err := hasSent(ctx, tx, id)
		if err != nil {
			return View{}, err
		}
		// As cancelling does: one that sent is closed by the worker with what it sent,
		// one that never did goes back to approved.
		next := statusApproved
		if sentBefore {
			next = statusDispatching
			_, err = tx.Exec(ctx, `UPDATE campaign_schedules SET next_run_at = NULL WHERE campaign_id = $1`, id)
		} else {
			_, err = tx.Exec(ctx, `DELETE FROM campaign_schedules WHERE campaign_id = $1`, id)
		}
		if err != nil {
			return View{}, fmt.Errorf("cancel schedule: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE campaigns SET status = $2 WHERE id = $1`, id, next); err != nil {
			return View{}, fmt.Errorf("update status: %w", err)
		}
		details["schedule_cancelled"] = true
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET archived_at = $2, updated_at = now() WHERE id = $1`, id, s.now()); err != nil {
		return View{}, fmt.Errorf("archive campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventArchived, details); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit archive: %w", err)
	}
	return s.Get(ctx, id)
}

// Unarchive brings a campaign back to the list as it was. A cancelled schedule stays
// cancelled; choosing a time again is a decision of its own.
func (s *Service) Unarchive(ctx context.Context, id string) (View, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin unarchive: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, archived, err := lockForArchive(ctx, tx, id); err != nil {
		return View{}, err
	} else if !archived {
		return View{}, ErrNotArchived
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET archived_at = NULL, updated_at = now() WHERE id = $1`, id); err != nil {
		return View{}, fmt.Errorf("unarchive campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventRestored, map[string]any{}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit unarchive: %w", err)
	}
	return s.Get(ctx, id)
}

// Delete removes a campaign that never sent from every list and page. One that sent
// keeps a record of who got what, which the rules about messaging people again rely
// on, so it can only be archived. The audit trail cannot change, so the campaign's
// row stays behind it with the time it was deleted.
func (s *Service) Delete(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, _, err := lockForArchive(ctx, tx, id)
	if err != nil {
		return err
	}
	if status == statusGenerating {
		return ErrGenerationRunning
	}
	var sent bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM campaign_runs WHERE campaign_id = $1)
		    OR EXISTS (SELECT 1 FROM campaign_schedules WHERE campaign_id = $1)`, id).Scan(&sent); err != nil {
		return fmt.Errorf("check sends: %w", err)
	}
	if sent || !slices.Contains([]string{statusDraft, statusReady, statusFailed, statusApproved, "CANCELLED"}, status) {
		return ErrSentBefore
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET deleted_at = $2, updated_at = now() WHERE id = $1`, id, s.now()); err != nil {
		return fmt.Errorf("delete campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventDeleted, map[string]any{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete: %w", err)
	}
	return nil
}
