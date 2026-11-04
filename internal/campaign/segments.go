package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	eventAudienceSaved = "audience.saved"
	// triggerSavedAudience marks a rule that no model proposed.
	triggerSavedAudience = "saved_audience"
	uniqueViolation      = "23505"
)

var (
	// ErrSegmentNotFound means a saved audience does not exist.
	ErrSegmentNotFound = errors.New("saved audience not found")
	// ErrSegmentName means a saved audience's name is empty or too long.
	ErrSegmentName = errors.New("invalid saved audience name")
	// ErrSegmentNameTaken means another saved audience has this name.
	ErrSegmentNameTaken = errors.New("saved audience name taken")
	// ErrSegmentNotSavable means a campaign has no rule fit to keep.
	ErrSegmentNotSavable = errors.New("campaign has no audience to save")
)

// SavedSegment is a rule kept for reuse. The counts are from CountedAt, not from now.
type SavedSegment struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Conditions         []segment.Condition `json:"conditions"`
	Audience           segment.Counts      `json:"audience"`
	CountedAt          time.Time           `json:"counted_at"`
	SourceCampaignID   *string             `json:"source_campaign_id"`
	SourceCampaignName *string             `json:"source_campaign_name"`
	CreatedBy          *string             `json:"created_by"`
	CreatedAt          time.Time           `json:"created_at"`
}

// SaveSegment keeps a campaign's current rule under a name. The rule comes from the
// campaign, where it has passed validation, and never from the request.
func (s *Service) SaveSegment(ctx context.Context, campaignID, name string) (SavedSegment, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > segment.MaxCampaignNameLength {
		return SavedSegment{}, ErrSegmentName
	}
	view, err := s.Get(ctx, campaignID)
	if err != nil {
		return SavedSegment{}, err
	}
	if view.Proposal == nil || len(view.Blockers) > 0 {
		return SavedSegment{}, ErrSegmentNotSavable
	}
	spec, err := json.Marshal(view.Proposal.Segment)
	if err != nil {
		return SavedSegment{}, fmt.Errorf("encode segment: %w", err)
	}
	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, view.Proposal.Segment, asOf)
	if err != nil {
		return SavedSegment{}, err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return SavedSegment{}, fmt.Errorf("begin save segment: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO saved_segments (name, spec, source_campaign_id, created_by, total, sms, email, counted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id::text`,
		name, spec, campaignID, personFrom(ctx), counts.Total, counts.SMS, counts.Email, asOf).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return SavedSegment{}, ErrSegmentNameTaken
	}
	if err != nil {
		return SavedSegment{}, fmt.Errorf("save segment: %w", err)
	}
	if err := recordAudit(ctx, tx, campaignID, eventAudienceSaved, map[string]any{"name": name, "audience": counts}); err != nil {
		return SavedSegment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SavedSegment{}, fmt.Errorf("commit save segment: %w", err)
	}
	return s.savedSegment(ctx, id)
}

const savedSegmentSelect = `
	SELECT s.id::text, s.name, s.spec, s.total, s.sms, s.email, s.counted_at, s.source_campaign_id::text, c.name, s.created_by, s.created_at
	FROM saved_segments s LEFT JOIN campaigns c ON c.id = s.source_campaign_id `

func scanSavedSegment(row pgx.CollectableRow) (SavedSegment, error) {
	var (
		saved   SavedSegment
		encoded []byte
	)
	if err := row.Scan(&saved.ID, &saved.Name, &encoded, &saved.Audience.Total, &saved.Audience.SMS, &saved.Audience.Email, &saved.CountedAt,
		&saved.SourceCampaignID, &saved.SourceCampaignName, &saved.CreatedBy, &saved.CreatedAt); err != nil {
		return SavedSegment{}, err
	}
	var spec segment.Spec
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return SavedSegment{}, fmt.Errorf("decode segment: %w", err)
	}
	saved.Conditions = segment.Describe(spec)
	return saved, nil
}

// SavedSegments lists the library by name.
func (s *Service) SavedSegments(ctx context.Context) ([]SavedSegment, error) {
	rows, err := s.db.Query(ctx, savedSegmentSelect+`ORDER BY lower(s.name)`)
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}
	saved, err := pgx.CollectRows(rows, scanSavedSegment)
	if err != nil {
		return nil, fmt.Errorf("list segments: %w", err)
	}
	return saved, nil
}

func (s *Service) savedSegment(ctx context.Context, id string) (SavedSegment, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return SavedSegment{}, ErrSegmentNotFound
	}
	rows, err := s.db.Query(ctx, savedSegmentSelect+`WHERE s.id = $1`, parsed)
	if err != nil {
		return SavedSegment{}, fmt.Errorf("read segment: %w", err)
	}
	saved, err := pgx.CollectExactlyOneRow(rows, scanSavedSegment)
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedSegment{}, ErrSegmentNotFound
	}
	if err != nil {
		return SavedSegment{}, fmt.Errorf("read segment: %w", err)
	}
	return saved, nil
}

// savedRule reads what a saved audience selects. The rule passed validation when it was
// saved, so using it does not need a model.
func (s *Service) savedRule(ctx context.Context, id string) (string, segment.Spec, error) {
	saved, err := s.savedSegment(ctx, id)
	if err != nil {
		return "", segment.Spec{}, err
	}
	var encoded []byte
	if err := s.db.QueryRow(ctx, `SELECT spec FROM saved_segments WHERE id = $1`, id).Scan(&encoded); err != nil {
		return "", segment.Spec{}, fmt.Errorf("read segment: %w", err)
	}
	var spec segment.Spec
	if err := json.Unmarshal(encoded, &spec); err != nil {
		return "", segment.Spec{}, fmt.Errorf("decode segment: %w", err)
	}
	return saved.Name, spec, nil
}

// useSavedRule puts a saved rule on a campaign, counted as of now. The campaign keeps its
// own copy, so changing or deleting the saved audience does not touch it.
func (s *Service) useSavedRule(ctx context.Context, id, brief string, briefChanged bool, proposal segment.Proposal) error {
	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, proposal.Segment, asOf)
	if err != nil {
		return err
	}
	return s.saveProposal(ctx, id, brief, briefChanged, proposal, counts, asOf, 0, triggerSavedAudience)
}

// RecountSegment sizes a saved rule again as of now.
func (s *Service) RecountSegment(ctx context.Context, id string) (SavedSegment, error) {
	_, spec, err := s.savedRule(ctx, id)
	if err != nil {
		return SavedSegment{}, err
	}
	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, spec, asOf)
	if err != nil {
		return SavedSegment{}, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE saved_segments SET total = $2, sms = $3, email = $4, counted_at = $5 WHERE id = $1`,
		id, counts.Total, counts.SMS, counts.Email, asOf); err != nil {
		return SavedSegment{}, fmt.Errorf("update segment: %w", err)
	}
	return s.savedSegment(ctx, id)
}

// DeleteSegment removes a saved rule. Campaigns made with it keep their own copy.
func (s *Service) DeleteSegment(ctx context.Context, id string) error {
	if _, err := s.savedSegment(ctx, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM saved_segments WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete segment: %w", err)
	}
	return nil
}
