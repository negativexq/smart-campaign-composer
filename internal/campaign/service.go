// Package campaign owns the campaign lifecycle: it asks a provider for a
// proposal, passes it through validation and records what was decided.
package campaign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/language"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

const (
	// MaxBriefLength limits briefs, in characters.
	MaxBriefLength = 2000
	// MaxProposalAttempts is the first try plus repairs of invalid output.
	MaxProposalAttempts = 3

	statusDraft        = "DRAFT"
	operationSegment   = "segment_generation"
	llmCallTimeout     = 90 * time.Second
	stageSegment       = "segment"
	stageAudience      = "audience"
	outcomeSuccess     = "success"
	outcomeInvalid     = "invalid_output"
	reasonUnsupported  = "unsupported_condition"
	reasonUnmatched    = "brief_not_matched"
	reasonEmpty        = "empty_audience"
	reasonNoConsent    = "no_consented_recipients"
	eventRenamed       = "campaign.renamed"
	eventLanguages     = "campaign.languages_changed"
	maxProvisionalName = 60
	eventCreated       = "campaign.created"
	eventDuplicated    = "campaign.duplicated"
	eventSegmentChosen = "segment.proposed"
)

// Errors the API maps to responses.
var (
	ErrInvalidBrief     = errors.New("brief is empty or too long")
	ErrInvalidChannels  = errors.New("channels must be sms, email or both")
	ErrInvalidLanguages = errors.New("only languages we write in, other than English, can be left out")
	ErrInvalidOptions   = errors.New("tone, offer or exclusion is not valid")
	ErrOfferConflict    = errors.New("brief mentions a different discount than the offer")
	ErrCodeConflict     = errors.New("brief names a different discount code than the form, or more than one")
	ErrLinkConflict     = errors.New("brief names more than one link")
	ErrInvalidName      = errors.New("campaign name is empty or too long")
	ErrNotFound         = errors.New("campaign not found")
	ErrNotEditable      = errors.New("campaign can no longer be edited")
	ErrProposalRejected = errors.New("proposal failed validation")
)

// ProposeInput changes what is proposed. Brief replaces the brief and starts
// over; Refinement adjusts the current proposal.
type ProposeInput struct {
	Brief      *string
	Refinement string
}

// View is a campaign as the marketer sees it.
type View struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	NameSource string `json:"name_source"`
	// EmailTemplate is the design chosen before any message was written.
	EmailTemplate string `json:"email_template"`
	// EmailDesign is the campaign's copy of a marketer's own design, when it started from one.
	EmailDesign *content.Design `json:"email_design,omitempty"`
	Brief       string          `json:"brief"`
	Status      string          `json:"status"`
	// Tags group campaigns the way a team thinks of them.
	Tags []string `json:"tags"`
	// ArchivedAt is when the campaign left the list, if it did.
	ArchivedAt *time.Time `json:"archived_at"`
	Channels   []string   `json:"channels"`
	// ExcludedLanguages get no version of their own, and their readers get English.
	ExcludedLanguages []string `json:"excluded_languages"`
	Tone              string   `json:"tone"`
	ABTest            bool     `json:"ab_test"`
	VariantAPercent   int      `json:"variant_a_percent"`
	OfferPercent      *int     `json:"offer_percent"`
	// FormDiscountCode is the code chosen on the form, if one was.
	FormDiscountCode *string `json:"form_discount_code"`
	// DiscountCode is the code every message carries: the form's, or the one the brief
	// names. Empty when there is none.
	DiscountCode string `json:"discount_code"`
	// Link is the brief's link: every SMS carries it as a short link and every email's
	// button opens it. Empty when the brief has none.
	Link string `json:"link"`
	// NotMessagedWithinDays is what the form asked for; the audience rule that
	// applies is in Proposal.
	NotMessagedWithinDays *int                `json:"not_messaged_within_days"`
	CreatedBy             *string             `json:"created_by"`
	Version               int                 `json:"version"`
	Provider              string              `json:"provider,omitempty"`
	Model                 string              `json:"model,omitempty"`
	Proposal              *segment.Proposal   `json:"proposal,omitempty"`
	Conditions            []segment.Condition `json:"conditions"`
	Audience              segment.Counts      `json:"audience"`
	AudienceAsOf          time.Time           `json:"audience_as_of,omitzero"`
	AudienceSnapshot      *AudienceSnapshot   `json:"audience_snapshot,omitempty"`
	Blockers              []string            `json:"blockers"`
	Content               *StoredContent      `json:"content,omitempty"`
	// ContentLimits is how long {{first_name}} can get in this audience.
	ContentLimits content.Limits `json:"content_limits"`
	// ContentWarnings are differences from the brief for a person to judge.
	ContentWarnings []content.Issue `json:"content_warnings"`
	// Languages splits the audience by the language people read.
	Languages []segment.LanguageAudience `json:"languages"`
	// VariantAudience is who lands on each side of a test.
	VariantAudience []VariantAudience `json:"variant_audience,omitempty"`
	ApprovedAt      time.Time         `json:"approved_at,omitzero"`
	Schedule        *ScheduleView     `json:"schedule,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	// ContentRevision names the stored messages as they are now. A save sends back
	// the one it was made to, so it cannot undo a newer change.
	ContentRevision string `json:"content_revision,omitempty"`
	// ReviewRevision names everything an approval freezes: the messages, the audience
	// rule and the time it was counted at, the brief and the languages left out. The
	// review page sends back the one it showed, so nobody approves what they did not see.
	ReviewRevision string `json:"review_revision,omitempty"`
}

// Service runs campaign operations.
type Service struct {
	db       *pgxpool.Pool
	provider ai.Provider
	metrics  *telemetry.Metrics
	tracer   trace.Tracer
	now      func() time.Time
	channels channelUseCache
	live     liveCache
}

// NewService builds a service.
func NewService(db *pgxpool.Pool, provider ai.Provider, metrics *telemetry.Metrics, tracer trace.Tracer, now func() time.Time) *Service {
	return &Service{db: db, provider: provider, metrics: metrics, tracer: tracer, now: now}
}

// Create stores a new draft and returns its ID.
func (s *Service) Create(ctx context.Context, brief string, chosen ...string) (string, error) {
	return s.CreateWith(ctx, NewCampaign{Brief: brief, Channels: chosen})
}

// NewCampaign is what a marketer fills in. Only the brief is required.
type NewCampaign struct {
	Brief        string
	Channels     []string
	Tone         string
	OfferPercent *int
	// DiscountCode goes into every message as written. Letters are made capitals.
	DiscountCode          *string
	NotMessagedWithinDays *int
	ABTest                bool
	// VariantAPercent is who gets version A, from 1 to 99. Half when left out.
	VariantAPercent *int
	// EmailTemplate is the design to write into, when one was chosen up front.
	EmailTemplate string
	// SavedAudienceID takes the rule from a saved audience, so the brief only has to say
	// what the message is about.
	SavedAudienceID string
}

func (n NewCampaign) validate() error {
	if n.EmailTemplate != "" && !slices.Contains(content.EmailTemplates, n.EmailTemplate) && !content.IsCustomTemplate(n.EmailTemplate) {
		return ErrInvalidOptions
	}
	if !slices.Contains([]string{"", "friendly", "formal", "urgent"}, n.Tone) {
		return ErrInvalidOptions
	}
	if n.OfferPercent != nil && (*n.OfferPercent < 1 || *n.OfferPercent > 100) {
		return ErrInvalidOptions
	}
	if n.DiscountCode != nil && !content.ValidCode(*n.DiscountCode) {
		return ErrInvalidOptions
	}
	if n.NotMessagedWithinDays != nil && (*n.NotMessagedWithinDays < 1 || *n.NotMessagedWithinDays > 365) {
		return ErrInvalidOptions
	}
	if n.VariantAPercent != nil && (!n.ABTest || *n.VariantAPercent < 1 || *n.VariantAPercent > 99) {
		return ErrInvalidOptions
	}
	return checkOffer(n.Brief, n.OfferPercent, n.DiscountCode)
}

// checkOffer refuses two different offers rather than guessing which one is meant,
// and the same for codes, where sending the wrong one is worse than sending none.
func checkOffer(brief string, offer *int, code *string) error {
	if offer != nil && !content.OfferAgrees(brief, *offer) {
		return ErrOfferConflict
	}
	codes := content.BriefCodes(brief)
	if len(codes) > 1 || (code != nil && len(codes) == 1 && codes[0] != *code) {
		return ErrCodeConflict
	}
	// Every SMS carries one short link, so a brief with two would leave one out.
	if len(content.BriefLinks(brief)) > 1 {
		return ErrLinkConflict
	}
	return nil
}

// cleanCode makes a code's letters capitals and treats a blank one as none.
func cleanCode(code *string) *string {
	if code == nil {
		return nil
	}
	cleaned := strings.ToUpper(strings.TrimSpace(*code))
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

// discountCode is the code the messages carry: the form's, or the brief's own.
func discountCode(brief string, form *string) string {
	if form != nil {
		return *form
	}
	if codes := content.BriefCodes(brief); len(codes) == 1 {
		return codes[0]
	}
	return ""
}

// briefLink is the link the messages carry, or empty.
func briefLink(brief string) string {
	if links := content.BriefLinks(brief); len(links) == 1 {
		return links[0]
	}
	return ""
}

// CreateWith stores a new draft with the choices made on the form.
func (s *Service) CreateWith(ctx context.Context, campaign NewCampaign) (string, error) {
	brief, err := cleanBrief(campaign.Brief)
	if err != nil {
		return "", err
	}
	chosen, err := normalizeChannels(campaign.Channels)
	if err != nil {
		return "", err
	}
	campaign.DiscountCode = cleanCode(campaign.DiscountCode)
	if err := campaign.validate(); err != nil {
		return "", err
	}
	var (
		design       []byte
		designAssets []string
	)
	if content.IsCustomTemplate(campaign.EmailTemplate) {
		chosen, err := s.designForTemplate(ctx, campaign.EmailTemplate)
		if errors.Is(err, ErrDesignNotFound) {
			return "", ErrInvalidOptions
		}
		if err != nil {
			return "", err
		}
		designAssets = chosen.AssetIDs()
		if design, err = json.Marshal(chosen); err != nil {
			return "", fmt.Errorf("encode design: %w", err)
		}
	}
	var (
		savedName string
		savedSpec segment.Spec
	)
	if campaign.SavedAudienceID != "" {
		if savedName, savedSpec, err = s.savedRule(ctx, campaign.SavedAudienceID); err != nil {
			return "", err
		}
		if campaign.NotMessagedWithinDays != nil {
			savedSpec.NotMessagedWithinDays = campaign.NotMessagedWithinDays
		}
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin create: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	// The copy was read from the library before this; its images must still be there.
	if err := lockAssets(ctx, tx, designAssets); err != nil {
		return "", err
	}

	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO campaigns (brief, name, created_by, channels, tone, offer_percent, not_messaged_within_days, ab_test, variant_a_percent, email_template, email_design, discount_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9::int, 50), $10, $11, $12) RETURNING id::text`,
		brief, provisionalName(brief), personFrom(ctx), chosen, campaign.Tone, campaign.OfferPercent, campaign.NotMessagedWithinDays, campaign.ABTest, campaign.VariantAPercent, campaign.EmailTemplate, design, campaign.DiscountCode).Scan(&id); err != nil {
		return "", fmt.Errorf("insert campaign: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventCreated, map[string]any{"brief_characters": utf8.RuneCountInString(brief), "channels": chosen, "tone": campaign.Tone, "offer_percent": campaign.OfferPercent, "discount_code": campaign.DiscountCode, "not_messaged_within_days": campaign.NotMessagedWithinDays, "email_template": campaign.EmailTemplate}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit create: %w", err)
	}
	if campaign.SavedAudienceID != "" {
		proposal := segment.Proposal{SavedAudience: savedName, Segment: savedSpec, Assumptions: []segment.Assumption{}, Evidence: []segment.Evidence{}, UnsupportedConditions: []string{}}
		if err := s.useSavedRule(ctx, id, brief, false, proposal); err != nil {
			return "", err
		}
	}
	return id, nil
}

// DuplicatePreview is what a copy of a campaign would reach, counted now, and how
// many of those people the campaign itself already reached.
type DuplicatePreview struct {
	// NotMessagedWithinDays is the rule the counts were made with: the one asked
	// for, or else the campaign's own, which may be none.
	NotMessagedWithinDays *int `json:"not_messaged_within_days"`
	// SourceRule is the campaign's own rule, which a copy keeps unless told otherwise.
	SourceRule    *int           `json:"source_rule"`
	Audience      segment.Counts `json:"audience"`
	ReachedBefore int            `json:"reached_before"`
	LastSentAt    *time.Time     `json:"last_sent_at"`
}

// DuplicatePreview counts what Duplicate with the same rule would start from.
// rule nil keeps the campaign's own.
func (s *Service) DuplicatePreview(ctx context.Context, id string, rule *int) (DuplicatePreview, error) {
	if rule != nil && (*rule < 1 || *rule > 365) {
		return DuplicatePreview{}, ErrInvalidOptions
	}
	source, err := s.load(ctx, id)
	if err != nil {
		return DuplicatePreview{}, err
	}
	preview := DuplicatePreview{}
	if err := s.db.QueryRow(ctx, `
		SELECT max(sent_at) FROM campaign_deliveries WHERE campaign_id = $1 AND status IN ('SENT', 'UNKNOWN')`, id).Scan(&preview.LastSentAt); err != nil {
		return DuplicatePreview{}, fmt.Errorf("read last send: %w", err)
	}
	if source.Proposal == nil {
		return preview, nil
	}
	spec := source.Proposal.Segment
	preview.SourceRule = spec.NotMessagedWithinDays
	if rule != nil {
		spec.NotMessagedWithinDays = rule
	}
	preview.NotMessagedWithinDays = spec.NotMessagedWithinDays
	now := s.now()
	if preview.Audience, err = segment.Count(ctx, s.db, spec, now); err != nil {
		return DuplicatePreview{}, err
	}
	where, args := segment.Compile(spec, now)
	err = s.db.QueryRow(ctx, `
		SELECT count(*) FROM users u
		WHERE `+where+` AND EXISTS (
			SELECT 1 FROM campaign_deliveries d
			WHERE d.campaign_id = `+fmt.Sprintf("$%d", len(args)+1)+` AND d.user_id = u.id AND d.status IN ('SENT', 'UNKNOWN'))`,
		append(args, id)...).Scan(&preview.ReachedBefore)
	if err != nil {
		return DuplicatePreview{}, fmt.Errorf("count people reached before: %w", err)
	}
	return preview, nil
}

// copySuffix matches the end a copy's name gets, so copying a copy numbers it from
// the original name instead of adding another "(copy)".
var copySuffix = regexp.MustCompile(` \(copy(?: (\d+))?\)$`)

// copyName names a copy after the original: "Name (copy)", then "Name (copy 2)" and
// on, one past the highest number already in use.
func copyName(ctx context.Context, tx pgx.Tx, source string) (string, error) {
	base := copySuffix.ReplaceAllString(source, "")
	rows, err := tx.Query(ctx, `SELECT name FROM campaigns WHERE starts_with(name, $1 || ' (copy')`, base)
	if err != nil {
		return "", fmt.Errorf("find earlier copies: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", fmt.Errorf("find earlier copies: %w", err)
	}
	highest := 0
	for _, name := range names {
		match := copySuffix.FindStringSubmatch(name)
		if match == nil || strings.TrimSuffix(name, match[0]) != base {
			continue
		}
		number := 1
		if match[1] != "" {
			number, _ = strconv.Atoi(match[1]) //nolint:errcheck // the pattern holds digits only
		}
		highest = max(highest, number)
	}
	suffix := " (copy)"
	if highest > 0 {
		suffix = fmt.Sprintf(" (copy %d)", highest+1)
	}
	name := []rune(base)
	if limit := segment.MaxCampaignNameLength - len(suffix); len(name) > limit {
		name = name[:limit]
	}
	return string(name) + suffix, nil
}

// Duplicate starts a new draft from a campaign: its brief, options, audience rule and
// messages. Approval, schedule and deliveries stay behind, and the audience is counted
// again from now, so the copy goes through every check the original did. rule, when
// given, leaves out people any campaign messaged within that many days.
func (s *Service) Duplicate(ctx context.Context, id string, rule *int) (View, error) {
	if rule != nil && (*rule < 1 || *rule > 365) {
		return View{}, ErrInvalidOptions
	}
	source, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	status := statusDraft
	if source.Content != nil {
		status = statusReady
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin duplicate: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	name, err := copyName(ctx, tx, source.Name)
	if err != nil {
		return View{}, err
	}

	var copied string
	if err := tx.QueryRow(ctx, `
		INSERT INTO campaigns (tags, brief, name, name_source, created_by, channels, excluded_languages, tone, offer_percent, discount_code, not_messaged_within_days, ab_test, variant_a_percent, email_template, email_design, status)
		SELECT tags, brief, $2, name_source, $3, channels, excluded_languages, tone, offer_percent, discount_code, COALESCE($5, not_messaged_within_days), ab_test, variant_a_percent, email_template, email_design, $4
		FROM campaigns WHERE id = $1
		RETURNING id::text`, id, name, personFrom(ctx), status, rule).Scan(&copied); err != nil {
		return View{}, fmt.Errorf("copy campaign: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_versions (campaign_id, version, segment, content, llm_provider, llm_model, audience_as_of)
		SELECT $2, 1,
		       CASE WHEN $4::int IS NULL OR segment IS NULL THEN segment
		            ELSE jsonb_set(segment, '{segment,not_messaged_within_days}', to_jsonb($4::int)) END,
		       content, llm_provider, llm_model, $3
		FROM campaign_versions WHERE campaign_id = $1
		ORDER BY version DESC LIMIT 1`, id, copied, s.now(), rule); err != nil {
		return View{}, fmt.Errorf("copy version: %w", err)
	}
	if err := recordAudit(ctx, tx, copied, eventDuplicated, map[string]any{"source": id, "source_name": source.Name, "not_messaged_within_days": rule}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit duplicate: %w", err)
	}
	return s.Get(ctx, copied)
}

// Rename sets the name someone chose. Names label a campaign rather than being
// part of the approved content, so this works in every status.
func (s *Service) Rename(ctx context.Context, id, name string) (View, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return View{}, ErrNotFound
	}
	name, ok := segment.CleanCampaignName(name)
	if !ok {
		return View{}, ErrInvalidName
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin rename: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	result, err := tx.Exec(ctx, `UPDATE campaigns SET name = $2, name_source = 'user', updated_at = now() WHERE id = $1`, parsed, name)
	if err != nil {
		return View{}, fmt.Errorf("rename campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return View{}, ErrNotFound
	}
	if err := recordAudit(ctx, tx, id, eventRenamed, map[string]any{"name": name}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit rename: %w", err)
	}
	return s.Get(ctx, id)
}

// SetExcludedLanguages chooses which languages get no version. It says nothing about
// who receives the campaign: their readers stay in the audience and get English.
// Versions of a language left out are removed, since nobody would read them.
func (s *Service) SetExcludedLanguages(ctx context.Context, id string, excluded []string) (View, error) {
	chosen := []string{}
	for _, code := range excluded {
		if !language.Known(code) || code == language.Fallback {
			return View{}, ErrInvalidLanguages
		}
		if !slices.Contains(chosen, code) {
			chosen = append(chosen, code)
		}
	}
	slices.Sort(chosen)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin languages: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	status, err := lockStatus(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	if status != statusDraft && status != statusReady && status != statusFailed {
		return View{}, ErrNotEditable
	}
	stored, err := s.loadFrom(ctx, tx, id)
	if err != nil {
		return View{}, err
	}
	// A draft from before languages were kept has one version nobody can tell apart.
	if stored.Content != nil && stored.Content.Language != "" {
		kept := stored.Content.without(chosen)
		// With no version left the campaign has no messages, stored the way a new
		// audience leaves it, since the column cannot be empty.
		encoded := []byte("{}")
		if kept != nil {
			if encoded, err = json.Marshal(kept); err != nil {
				return View{}, fmt.Errorf("encode content: %w", err)
			}
		} else {
			status = statusDraft
		}
		if _, err := tx.Exec(ctx, `UPDATE campaign_versions SET content = $3 WHERE campaign_id = $1 AND version = $2`, id, stored.Version, encoded); err != nil {
			return View{}, fmt.Errorf("remove language versions: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET excluded_languages = $2, status = $3, updated_at = now() WHERE id = $1`, id, chosen, status); err != nil {
		return View{}, fmt.Errorf("set languages: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventLanguages, map[string]any{"excluded": chosen}); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit languages: %w", err)
	}
	return s.Get(ctx, id)
}

// provisionalName labels a campaign until a better name exists, cutting the
// brief between words.
func provisionalName(brief string) string {
	words := strings.Fields(brief)
	name := strings.Join(words, " ")
	if utf8.RuneCountInString(name) <= maxProvisionalName {
		return name
	}
	var kept strings.Builder
	for _, word := range words {
		if utf8.RuneCountInString(kept.String())+utf8.RuneCountInString(word)+1 > maxProvisionalName-1 {
			break
		}
		if kept.Len() > 0 {
			kept.WriteByte(' ')
		}
		kept.WriteString(word)
	}
	if kept.Len() == 0 {
		kept.WriteString(string([]rune(name)[:maxProvisionalName-1]))
	}
	return kept.String() + "…"
}

// Unsubscribe withdraws a person's email consent. Deliveries still waiting read
// consent again before sending, so they stop as well.
func (s *Service) Unsubscribe(ctx context.Context, userID int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE consents SET allowed = false, updated_at = now() WHERE user_id = $1 AND channel = 'email' AND allowed`, userID); err != nil {
		return fmt.Errorf("withdraw email consent: %w", err)
	}
	return nil
}

// writingBrief is the brief as a message writer and its checks see it, with the
// offer from the form stated like any other.
func (v View) writingBrief() string {
	brief := v.Brief
	if v.OfferPercent != nil {
		brief = fmt.Sprintf("%s\n\nOffer set by the marketer: %d%% discount.", brief, *v.OfferPercent)
	}
	if v.DiscountCode != "" {
		brief = fmt.Sprintf("%s\n\nDiscount code set by the marketer: %s. Write {{%s}} where the code goes; the code is filled in when the message is sent.", brief, v.DiscountCode, content.CodeVariable)
	}
	if v.Link != "" {
		brief = fmt.Sprintf("%s\n\nLink: %s. Put it on the email's button exactly as written, and write {{%s}} in the SMS where it goes; each SMS gets a short link in its place when it is sent.", brief, v.Link, content.LinkVariable)
	}
	return brief
}

// normalizeChannels puts a choice in the product's order. Saying nothing means
// both, while an empty list is a choice of none and is refused.
func normalizeChannels(chosen []string) ([]string, error) {
	if chosen == nil {
		return []string{"sms", "email"}, nil
	}
	normalized := make([]string, 0, 2)
	for _, channel := range []string{"sms", "email"} {
		if slices.Contains(chosen, channel) {
			normalized = append(normalized, channel)
		}
	}
	for _, channel := range chosen {
		if !slices.Contains(normalized, channel) {
			return nil, ErrInvalidChannels
		}
	}
	if len(normalized) == 0 {
		return nil, ErrInvalidChannels
	}
	return normalized, nil
}

func cleanBrief(brief string) (string, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" || utf8.RuneCountInString(brief) > MaxBriefLength {
		return "", ErrInvalidBrief
	}
	return brief, nil
}

// ProposeSegment asks the provider for an audience, repairs invalid output a
// bounded number of times and stores the result as the draft version. Output
// that never validates is not stored.
func (s *Service) ProposeSegment(ctx context.Context, id string, input ProposeInput) (view View, err error) {
	ctx, span := s.tracer.Start(ctx, "campaign.generate", trace.WithAttributes(attribute.String("campaign.id", id)))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "generation failed")
		}
		span.End()
	}()

	stored, err := s.load(ctx, id)
	if err != nil {
		return View{}, err
	}
	// Checked before the model is asked, and again when the result is saved.
	if stored.ArchivedAt != nil {
		return View{}, ErrArchived
	}
	switch {
	case stored.Status == statusDraft, stored.Status == statusReady, stored.Status == statusFailed, generationAbandoned(stored):
	case stored.Status == statusGenerating:
		return View{}, ErrGenerationRunning
	default:
		return View{}, ErrNotEditable
	}

	// A saved rule stays until someone asks to change it: its brief says what the message
	// is about, so there is no audience for a model to read from it.
	if stored.Proposal != nil && stored.Proposal.SavedAudience != "" && input.Refinement == "" {
		brief := stored.Brief
		if input.Brief != nil {
			if brief, err = cleanBrief(*input.Brief); err != nil {
				return View{}, err
			}
			if err := checkOffer(brief, stored.OfferPercent, stored.FormDiscountCode); err != nil {
				return View{}, err
			}
		}
		if err := s.useSavedRule(ctx, id, brief, brief != stored.Brief, *stored.Proposal); err != nil {
			return View{}, err
		}
		return s.Get(ctx, id)
	}

	request := ai.SegmentRequest{Brief: stored.Brief, Schema: segment.JSONSchema(), Refinement: input.Refinement}
	if input.Brief != nil {
		if request.Brief, err = cleanBrief(*input.Brief); err != nil {
			return View{}, err
		}
		if err := checkOffer(request.Brief, stored.OfferPercent, stored.FormDiscountCode); err != nil {
			return View{}, err
		}
		request.Refinement = ""
	} else if input.Refinement != "" && stored.Proposal != nil {
		current := *stored.Proposal
		current.Unmatched, current.NotApplied, current.Unsure, current.BriefParts, current.SavedAudience = nil, nil, nil, nil, ""
		if request.Current, err = json.Marshal(current); err != nil {
			return View{}, fmt.Errorf("encode current proposal: %w", err)
		}
	}
	if request.Cities, err = s.cities(ctx); err != nil {
		return View{}, err
	}

	proposal, attempts, err := s.generateProposal(ctx, request, stored.NotMessagedWithinDays)
	if err != nil {
		return View{}, err
	}
	brief := request.Brief
	if request.Refinement != "" {
		brief = proposal.UpdatedBrief
		if err := checkOffer(brief, stored.OfferPercent, stored.FormDiscountCode); err != nil {
			return View{}, err
		}
	}
	proposal.UpdatedBrief, proposal.SavedAudience = "", ""
	// A refinement starts from the current rule, so a person can still ask to drop it.
	if request.Refinement == "" && stored.NotMessagedWithinDays != nil {
		proposal.Segment.NotMessagedWithinDays = stored.NotMessagedWithinDays
	}

	s.checkEvidence(&proposal, request, stored.Proposal)
	if request.Refinement != "" {
		noteNotApplied(&proposal, stored, brief, request)
	}

	asOf := s.now()
	counts, err := s.evaluateAudience(ctx, proposal.Segment, asOf)
	if err != nil {
		return View{}, err
	}
	for _, blocker := range blockersFor(proposal, counts) {
		s.metrics.ValidationFailures.WithLabelValues(stageAudience, blocker.reason).Inc()
	}
	if err := s.saveProposal(ctx, id, brief, brief != stored.Brief, proposal, counts, asOf, attempts, proposalTrigger(input)); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// checkEvidence keeps the quotes a person really wrote. A refinement builds on
// the current proposal, so quotes already checked for it still count.
func (s *Service) checkEvidence(proposal *segment.Proposal, request ai.SegmentRequest, current *segment.Proposal) {
	sources := []string{request.Brief, request.Refinement}
	if request.Refinement != "" && current != nil {
		for _, evidence := range current.Evidence {
			sources = append(sources, evidence.Quote)
		}
	}
	if dropped := proposal.KeepQuotedEvidence(sources...); dropped > 0 {
		s.metrics.ValidationFailures.WithLabelValues(stageSegment, "unquoted_evidence").Add(float64(dropped))
	}
}

// proposalTrigger says why a proposal was made, for the audit trail.
func proposalTrigger(input ProposeInput) string {
	switch {
	case input.Brief != nil:
		return "brief_edit"
	case input.Refinement != "":
		return "refinement"
	default:
		return "brief"
	}
}

func (s *Service) generateProposal(ctx context.Context, request ai.SegmentRequest, chosenQuietDays *int) (segment.Proposal, int, error) {
	var lastInvalid *segment.ValidationError
	for attempt := 1; attempt <= MaxProposalAttempts; attempt++ {
		response, err := s.callProvider(ctx, request, attempt)
		if err != nil {
			return segment.Proposal{}, attempt, err
		}
		proposal, err := s.validateProposal(ctx, response.Output, request.Cities)
		if err == nil {
			err = checkUpdatedBrief(&proposal, request)
		}
		if err == nil {
			err = checkAgainstBrief(&proposal, request, chosenQuietDays)
		}
		if err == nil {
			s.metrics.LLMRequests.WithLabelValues(operationSegment, s.provider.Name(), outcomeSuccess).Inc()
			return proposal, attempt, nil
		}
		if !errors.As(err, &lastInvalid) {
			return segment.Proposal{}, attempt, err
		}
		s.metrics.LLMRequests.WithLabelValues(operationSegment, s.provider.Name(), outcomeInvalid).Inc()
		for _, reason := range lastInvalid.Reasons {
			s.metrics.ValidationFailures.WithLabelValues(stageSegment, reason).Inc()
		}
		slog.WarnContext(ctx, "proposal rejected", "attempt", attempt, "reasons", lastInvalid.Reasons)
		request.PreviousOutput = string(response.Output)
		request.ValidationError = lastInvalid.Error()
	}
	return segment.Proposal{}, MaxProposalAttempts, fmt.Errorf("%w: %w", ErrProposalRejected, lastInvalid)
}

// checkUpdatedBrief holds a rewritten brief to what the refinement asked. The
// rule and the brief must not disagree, so a bad rewrite rejects the proposal.
func checkUpdatedBrief(proposal *segment.Proposal, request ai.SegmentRequest) error {
	if request.Refinement == "" {
		return nil
	}
	updated, err := cleanBrief(proposal.UpdatedBrief)
	if err != nil {
		return &segment.ValidationError{
			Problems: []string{fmt.Sprintf("updated_brief must be the whole brief, at most %d characters", MaxBriefLength)},
			Reasons:  []string{"invalid_updated_brief"},
		}
	}
	if content.AddsOffersOrLinks(updated, request.Brief, request.Refinement) {
		return &segment.ValidationError{
			Problems: []string{"updated_brief must not add a discount or link that the brief and refinement do not contain"},
			Reasons:  []string{"invented_brief_facts"},
		}
	}
	proposal.UpdatedBrief = updated
	return nil
}

// noteNotApplied compares the result with what a refinement asked, so a request the
// model passed over is seen whether or not the model admits it.
func noteNotApplied(proposal *segment.Proposal, stored View, brief string, request ai.SegmentRequest) {
	unchanged := brief == stored.Brief && stored.Proposal != nil &&
		reflect.DeepEqual(proposal.Segment, stored.Proposal.Segment) &&
		slices.Equal(proposal.UnsupportedConditions, stored.Proposal.UnsupportedConditions)
	if unchanged {
		if len(proposal.NotApplied) > 0 {
			return // the model has already said so, and why
		}
		proposal.NotApplied = append(proposal.NotApplied, fmt.Sprintf("Nothing changed for your request: %q.", request.Refinement))
		return
	}
	result := strings.Join(slices.Concat([]string{brief}, proposal.UnsupportedConditions, proposal.NotApplied), "\n")
	for _, missing := range content.MissingFromInstruction(result, request.Refinement, request.Cities) {
		proposal.NotApplied = append(proposal.NotApplied, fmt.Sprintf("Your request mentions %s, but the result does not.", missing))
	}
}

// checkAgainstBrief compares the rule with the brief it will be saved with, which
// after a refinement is the rewritten one, so the two cannot drift apart.
func checkAgainstBrief(proposal *segment.Proposal, request ai.SegmentRequest, chosenQuietDays *int) error {
	brief := request.Brief
	if request.Refinement != "" {
		brief = proposal.UpdatedBrief
	}
	if problems := segment.CheckAgainstBrief(proposal, brief, request.Cities, chosenQuietDays); problems != nil {
		return problems
	}
	return nil
}

func (s *Service) callProvider(ctx context.Context, request ai.SegmentRequest, attempt int) (ai.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, llmCallTimeout)
	defer cancel()
	ctx, span := s.tracer.Start(ctx, "llm.segment.generate", trace.WithAttributes(
		attribute.String("llm.provider", s.provider.Name()),
		attribute.String("llm.model", s.provider.Model()),
		attribute.Int("llm.attempt", attempt),
	))
	defer span.End()

	start := time.Now()
	response, err := s.provider.ProposeSegment(ctx, request)
	name := s.provider.Name()
	s.metrics.LLMDuration.WithLabelValues(operationSegment, name).Observe(time.Since(start).Seconds())
	if err != nil {
		outcome := providerOutcome(err)
		s.metrics.LLMRequests.WithLabelValues(operationSegment, name, outcome).Inc()
		span.SetStatus(codes.Error, outcome)
		slog.WarnContext(ctx, "llm call failed", "provider", name, "outcome", outcome, "error", err)
		return ai.Response{}, err
	}
	s.metrics.LLMTokens.WithLabelValues(operationSegment, name, "input").Add(float64(response.Usage.InputTokens))
	s.metrics.LLMTokens.WithLabelValues(operationSegment, name, "output").Add(float64(response.Usage.OutputTokens))
	span.SetAttributes(
		attribute.Int64("llm.tokens.input", response.Usage.InputTokens),
		attribute.Int64("llm.tokens.output", response.Usage.OutputTokens),
	)
	return response, nil
}

func providerOutcome(err error) string {
	switch {
	case errors.Is(err, ai.ErrRefused):
		return "refused"
	case errors.Is(err, ai.ErrTruncated):
		return "truncated"
	case errors.Is(err, ai.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ai.ErrInvalidOutput):
		return outcomeInvalid
	default:
		return "unavailable"
	}
}

func (s *Service) validateProposal(ctx context.Context, output []byte, cities []string) (segment.Proposal, error) {
	_, span := s.tracer.Start(ctx, "segment.validate")
	defer span.End()
	proposal, err := segment.ParseProposal(output, cities)
	var invalid *segment.ValidationError
	if errors.As(err, &invalid) {
		span.SetStatus(codes.Error, "rejected")
		span.SetAttributes(attribute.StringSlice("validation.reasons", invalid.Reasons))
	}
	return proposal, err
}

func (s *Service) evaluateAudience(ctx context.Context, spec segment.Spec, asOf time.Time) (segment.Counts, error) {
	ctx, span := s.tracer.Start(ctx, "audience.evaluate")
	defer span.End()
	counts, err := segment.Count(ctx, s.db, spec, asOf)
	if err != nil {
		span.SetStatus(codes.Error, "count failed")
		return segment.Counts{}, err
	}
	span.SetAttributes(attribute.Int("audience.total", counts.Total))
	return counts, nil
}

// cities is the allowlist a proposal's city is checked against: whatever exists
// in user data, so the model cannot invent one.
func (s *Service) cities(ctx context.Context) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT city FROM users ORDER BY city`)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}
	return cities, nil
}

func (s *Service) saveProposal(ctx context.Context, id, brief string, briefChanged bool, proposal segment.Proposal, counts segment.Counts, asOf time.Time, attempts int, trigger string) error {
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("encode proposal: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// A campaign archived or deleted while the model answered keeps its audience.
	if _, err := lockStatus(ctx, tx, id); err != nil {
		return err
	}
	// A name someone chose stays. Otherwise a suggestion wins, and a provisional
	// name follows the brief it came from. Content was written for the previous
	// audience, so the campaign goes back to a draft without any.
	result, err := tx.Exec(ctx, `
		UPDATE campaigns SET brief = $2, status = 'DRAFT', generation_claim = NULL, updated_at = now(),
			name = CASE WHEN name_source = 'user' THEN name
			            WHEN $3::text <> '' THEN $3::text
			            WHEN name_source = 'brief' THEN $4::text
			            ELSE name END,
			name_source = CASE WHEN name_source = 'user' THEN 'user'
			                   WHEN $3::text <> '' THEN 'ai'
			                   ELSE name_source END
		WHERE id = $1 AND `+freeToDraft, id, brief, proposal.CampaignName, provisionalName(brief))
	if err != nil {
		return fmt.Errorf("update campaign: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrGenerationRunning
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO campaign_versions (campaign_id, version, segment, content, llm_provider, llm_model, audience_as_of)
		VALUES ($1, 1, $2, '{}', $3, $4, $5)
		ON CONFLICT (campaign_id, version) DO UPDATE
		SET segment = EXCLUDED.segment, content = '{}', llm_provider = EXCLUDED.llm_provider,
		    llm_model = EXCLUDED.llm_model, audience_as_of = EXCLUDED.audience_as_of`,
		id, encoded, s.provider.Name(), s.provider.Model(), asOf); err != nil {
		return fmt.Errorf("save version: %w", err)
	}
	if err := recordAudit(ctx, tx, id, eventSegmentChosen, map[string]any{
		"trigger": trigger, "brief_changed": briefChanged, "provider": s.provider.Name(), "model": s.provider.Model(), "attempts": attempts,
		"audience": counts, "assumptions": len(proposal.Assumptions),
		"unsupported_conditions": len(proposal.UnsupportedConditions),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit save: %w", err)
	}
	return nil
}

func recordAudit(ctx context.Context, tx pgx.Tx, id, event string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	actorType, actor := actorSystem, personFrom(ctx)
	if actor != nil {
		actorType = actorUser
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details, actor_type, actor) VALUES ($1, $2, $3, $4, $5)`, id, event, encoded, actorType, actor); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	return nil
}

// Get returns a campaign with its audience, counted at the time the proposal was
// made so the size under review does not drift as time windows slide.
func (s *Service) Get(ctx context.Context, id string) (View, error) {
	return s.get(ctx, s.db, id)
}

// get reads through q. A caller holding a transaction passes it, because waiting for
// a second connection while holding one hangs once every connection is held that way.
func (s *Service) get(ctx context.Context, q segment.Querier, id string) (View, error) {
	view, err := s.loadFrom(ctx, q, id)
	if err != nil {
		return View{}, err
	}
	if view.Schedule, err = s.loadSchedule(ctx, q, id); err != nil {
		return View{}, err
	}
	if view.Proposal == nil {
		return view, nil
	}
	view.Conditions = segment.Describe(view.Proposal.Segment)
	// An approved version reads its audience from the snapshot instead of counting the
	// customer base again. Snapshots from before languages were kept count them live.
	if snapshot := view.AudienceSnapshot; snapshot != nil {
		view.Audience = snapshot.Counts
		view.Languages = snapshot.Languages
		view.VariantAudience = snapshot.Variants
	} else if view.Audience, err = segment.Count(ctx, q, view.Proposal.Segment, view.AudienceAsOf); err != nil {
		return View{}, err
	}
	// Reachable means on a channel this campaign uses.
	if !slices.Contains(view.Channels, "sms") {
		view.Audience.SMS = 0
	}
	if !slices.Contains(view.Channels, "email") {
		view.Audience.Email = 0
	}
	for _, blocker := range blockersFor(*view.Proposal, view.Audience) {
		view.Blockers = append(view.Blockers, blocker.message)
	}
	if view.Languages == nil {
		if view.Languages, err = segment.Languages(ctx, q, view.Proposal.Segment, view.AudienceAsOf); err != nil {
			return View{}, err
		}
	}
	view.ContentLimits = content.Limits{SkipSMS: !slices.Contains(view.Channels, "sms"), SkipEmail: !slices.Contains(view.Channels, "email"), Code: view.DiscountCode, Link: view.Link}
	for i := range view.Languages {
		view.ContentLimits.SMSName = max(view.ContentLimits.SMSName, view.Languages[i].SMSName)
		view.ContentLimits.EmailName = max(view.ContentLimits.EmailName, view.Languages[i].EmailName)
		// Reachable means on a channel this campaign uses.
		if !slices.Contains(view.Channels, "sms") {
			view.Languages[i].SMS = 0
		}
		if !slices.Contains(view.Channels, "email") {
			view.Languages[i].Email = 0
		}
	}
	if view.ABTest && view.VariantAudience == nil {
		if view.VariantAudience, err = s.variantAudience(ctx, q, view); err != nil {
			return View{}, err
		}
	}
	if view.Content != nil {
		view.ContentWarnings = append(view.ContentWarnings, content.CheckDiscounts(view.Content.Content, view.writingBrief())...)
		view.ContentWarnings = append(view.ContentWarnings, content.CheckCode(view.Content.Content, view.limitsFor(view.Content.Language))...)
		view.ContentWarnings = append(view.ContentWarnings, content.CheckLink(view.Content.Content, view.limitsFor(view.Content.Language))...)
		view.ContentWarnings = append(view.ContentWarnings, view.Content.NotApplied...)
		for _, root := range view.Content.variants() {
			for index, version := range root.all() {
				if root == view.Content && index == 0 {
					continue
				}
				issues := append(content.CheckDiscounts(version.Content, view.writingBrief()), version.NotApplied...)
				issues = append(issues, content.CheckCode(version.Content, view.limitsFor(version.Language))...)
				issues = append(issues, content.CheckLink(version.Content, view.limitsFor(version.Language))...)
				for _, issue := range issues {
					issue.Message = versionLabel(version, index > 0) + ": " + issue.Message
					view.ContentWarnings = append(view.ContentWarnings, issue)
				}
			}
		}
		if slices.Contains([]string{statusDraft, statusGenerating, statusReady}, view.Status) {
			view.ContentWarnings = append(view.ContentWarnings, missingButton(view.Content)...)
		}
	}
	return view, nil
}

// missingButton says it once however many languages lack the button, since one link fixes all.
func missingButton(stored *StoredContent) []content.Issue {
	for _, root := range stored.variants() {
		for _, version := range root.all() {
			if issues := content.CheckButton(version.Content); len(issues) > 0 {
				return issues
			}
		}
	}
	return nil
}

func (s *Service) load(ctx context.Context, id string) (View, error) {
	return s.loadFrom(ctx, s.db, id)
}

func (s *Service) loadFrom(ctx context.Context, q segment.Querier, id string) (View, error) {
	var parsed pgtype.UUID
	if err := parsed.Scan(id); err != nil {
		return View{}, ErrNotFound
	}
	view := View{ID: id, Conditions: []segment.Condition{}, Blockers: []string{}, ContentWarnings: []content.Issue{}}
	var (
		version      *int
		provider     *string
		model        *string
		encoded      []byte
		storedText   []byte
		audienceAsOf *time.Time
		approvedAt   *time.Time
		snapshot     []byte
	)
	err := q.QueryRow(ctx, `
		SELECT c.name, c.name_source, c.email_template, c.email_design, c.brief, c.status, c.tags, c.archived_at, c.channels, c.excluded_languages, c.tone, c.ab_test, c.variant_a_percent, c.offer_percent, c.discount_code, c.not_messaged_within_days, c.created_by, c.created_at, c.updated_at, v.version, v.llm_provider, v.llm_model, v.segment, v.content, v.audience_as_of, v.approved_at, v.audience_snapshot
		FROM campaigns c
		LEFT JOIN LATERAL (
			SELECT * FROM campaign_versions WHERE campaign_id = c.id ORDER BY version DESC LIMIT 1
		) v ON true
		WHERE c.id = $1 AND c.deleted_at IS NULL`, parsed).Scan(&view.Name, &view.NameSource, &view.EmailTemplate, &view.EmailDesign, &view.Brief, &view.Status, &view.Tags, &view.ArchivedAt, &view.Channels, &view.ExcludedLanguages, &view.Tone, &view.ABTest, &view.VariantAPercent, &view.OfferPercent, &view.FormDiscountCode, &view.NotMessagedWithinDays, &view.CreatedBy, &view.CreatedAt, &view.UpdatedAt, &version, &provider, &model, &encoded, &storedText, &audienceAsOf, &approvedAt, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return View{}, ErrNotFound
	}
	if err != nil {
		return View{}, fmt.Errorf("load campaign: %w", err)
	}
	view.DiscountCode = discountCode(view.Brief, view.FormDiscountCode)
	view.Link = briefLink(view.Brief)
	if version == nil {
		return view, nil
	}
	view.Version = *version
	if provider != nil {
		view.Provider = *provider
	}
	if model != nil {
		view.Model = *model
	}
	if audienceAsOf != nil {
		view.AudienceAsOf = *audienceAsOf
	}
	if approvedAt != nil {
		view.ApprovedAt = *approvedAt
	}
	if snapshot != nil {
		view.AudienceSnapshot = &AudienceSnapshot{}
		if err := json.Unmarshal(snapshot, view.AudienceSnapshot); err != nil {
			return View{}, fmt.Errorf("decode audience snapshot: %w", err)
		}
	}
	view.Proposal = &segment.Proposal{}
	if err := json.Unmarshal(encoded, view.Proposal); err != nil {
		return View{}, fmt.Errorf("decode stored proposal: %w", err)
	}
	stored := StoredContent{}
	if err := json.Unmarshal(storedText, &stored); err != nil {
		return View{}, fmt.Errorf("decode stored content: %w", err)
	}
	if stored.Content != (content.Content{}) {
		view.Content = &stored
		view.ContentRevision = contentRevision(storedText)
		view.ReviewRevision = reviewRevision(storedText, encoded, view.AudienceAsOf, view.Brief, view.ExcludedLanguages)
	}
	return view, nil
}

// contentRevision is a short hash of the stored messages. PostgreSQL writes jsonb out
// the same way every time, so equal documents give equal revisions.
func contentRevision(stored []byte) string {
	sum := sha256.Sum256(stored)
	return hex.EncodeToString(sum[:8])
}

// reviewRevision is a short hash of what an approval freezes. Each part is written
// with its length first, so two different sets of parts cannot run together.
func reviewRevision(content, segment []byte, asOf time.Time, brief string, excluded []string) string {
	hash := sha256.New()
	for _, part := range [][]byte{content, segment, []byte(asOf.UTC().Format(time.RFC3339Nano)), []byte(brief), []byte(strings.Join(excluded, ","))} {
		fmt.Fprintf(hash, "%d:", len(part))
		hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil)[:8])
}

// AudienceSnapshot is the audience as a person approved it.
type AudienceSnapshot struct {
	Counts    segment.Counts    `json:"counts"`
	Breakdown segment.Breakdown `json:"breakdown"`
	// Languages and Variants are left out of snapshots made before they were kept.
	Languages []segment.LanguageAudience `json:"languages,omitempty"`
	Variants  []VariantAudience          `json:"variants,omitempty"`
}

type blocker struct {
	reason  string
	message string
}

// VariantAudience counts the people on one side of a test who read one language, and
// how many of them can be reached on each channel this campaign uses.
type VariantAudience struct {
	Variant  string `json:"variant"`
	Language string `json:"language"`
	Total    int    `json:"total"`
	SMS      int    `json:"sms"`
	Email    int    `json:"email"`
}

func (s *Service) variantAudience(ctx context.Context, q segment.Querier, view View) ([]VariantAudience, error) {
	where, args := segment.Compile(view.Proposal.Segment, view.AudienceAsOf)
	campaignParam, percentParam, channelsParam := fmt.Sprintf("$%d", len(args)+1), fmt.Sprintf("$%d", len(args)+2), fmt.Sprintf("$%d", len(args)+3)
	rows, err := q.Query(ctx, `
		SELECT `+variantOf(campaignParam, percentParam)+` AS variant, u.language, count(*),
		       count(*) FILTER (WHERE sms.allowed AND 'sms' = ANY(`+channelsParam+`::text[])),
		       count(*) FILTER (WHERE email.allowed AND 'email' = ANY(`+channelsParam+`::text[]))
		FROM users u
		LEFT JOIN consents sms ON sms.user_id = u.id AND sms.channel = 'sms'
		LEFT JOIN consents email ON email.user_id = u.id AND email.channel = 'email'
		WHERE `+where+`
		GROUP BY variant, u.language
		ORDER BY variant, count(*) DESC, u.language`, append(args, view.ID, view.VariantAPercent, view.Channels)...)
	if err != nil {
		return nil, fmt.Errorf("variant audience: %w", err)
	}
	counts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[VariantAudience])
	if err != nil {
		return nil, fmt.Errorf("variant audience: %w", err)
	}
	return counts, nil
}

// versionLabel names a version in a note about it.
func versionLabel(version *StoredContent, translated bool) string {
	parts := []string{}
	if version.Variant == content.VariantB {
		parts = append(parts, "Version B")
	}
	if translated {
		parts = append(parts, segment.LanguageName(version.Language)+" version")
	}
	return strings.Join(parts, ", ")
}

// writingLanguages is who needs a version: every language chosen with someone reachable
// on this campaign's channels, largest first, and English if anyone reachable reads a
// language that was not chosen or that we cannot write in.
func (v *View) writingLanguages() []string {
	languages := []string{}
	needsFallback := false
	for _, audience := range v.Languages {
		if audience.SMS+audience.Email == 0 {
			continue
		}
		if v.versionFor(audience.Language) == audience.Language {
			languages = append(languages, audience.Language)
		} else {
			needsFallback = true
		}
	}
	if len(languages) == 0 || (needsFallback && !slices.Contains(languages, language.Fallback)) {
		languages = append(languages, language.Fallback)
	}
	return languages
}

// versionFor is the language a reader of this one gets their message in.
func (v *View) versionFor(code string) string {
	if language.Known(code) && !slices.Contains(v.ExcludedLanguages, code) {
		return code
	}
	return language.Fallback
}

// limitsFor is the campaign's limits with the longest name among this version's
// readers, or the word that stands in for a missing name if that is longer.
func (v *View) limitsFor(code string) content.Limits {
	limits := v.ContentLimits
	fallback := utf8.RuneCountInString(content.FallbackName(code))
	sms, email, found := fallback, fallback, false
	for _, audience := range v.Languages {
		if v.versionFor(audience.Language) == code {
			sms, email, found = max(sms, audience.SMSName), max(email, audience.EmailName), true
		}
	}
	if found {
		limits.SMSName, limits.EmailName = sms, email
	}
	return limits
}

// blockersFor lists what must change before this audience can be approved.
func blockersFor(proposal segment.Proposal, counts segment.Counts) []blocker {
	// A rule with no conditions reaches nobody on purpose, so there is nothing to widen,
	// and what the model wrote about it is its reasoning, not something the brief asked.
	if proposal.Segment.IsEmpty() {
		return []blocker{{reasonEmpty, "No filter could be set from this brief, so it reaches nobody. Say who should receive it, for example a city or a recent purchase."}}
	}
	blockers := []blocker{}
	for _, condition := range proposal.UnsupportedConditions {
		blockers = append(blockers, blocker{reasonUnsupported, fmt.Sprintf(
			"The brief asks for %q, which no audience filter can express. Refine the audience to remove it.", condition)})
	}
	for _, note := range proposal.Unmatched {
		blockers = append(blockers, blocker{reasonUnmatched, fmt.Sprintf(
			"The brief mentions %s, but the rule does not use it. Refine the audience or edit the brief so they say the same thing.", note)})
	}
	switch {
	case counts.Total == 0:
		blockers = append(blockers, blocker{reasonEmpty, "No users match these conditions. Refine the audience to widen it."})
	case counts.SMS == 0 && counts.Email == 0:
		blockers = append(blockers, blocker{reasonNoConsent, fmt.Sprintf(
			"%s match these conditions, but none of them have consented to the channels this campaign uses.", peopleCount(counts.Total))})
	}
	return blockers
}

// peopleCount says how many people, for sentences.
func peopleCount(count int) string {
	if count == 1 {
		return "1 person"
	}
	return humanize(count) + " people"
}

// humanize groups thousands as the pages do, as in 7,000.
func humanize(count int) string {
	digits := strconv.Itoa(count)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}
