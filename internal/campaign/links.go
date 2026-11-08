package campaign

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"
)

// ErrUnknownLink is a short link no message carries.
var ErrUnknownLink = errors.New("short link not found")

var linkCodeShape = regexp.MustCompile(`^[A-Za-z0-9_-]{8}$`)

// FollowLink finds where a short link leads. A click counts it, so the report can say
// how many people opened the link; count is false for a look that is not a visit,
// such as a HEAD request.
func (s *Service) FollowLink(ctx context.Context, code string, count bool) (string, error) {
	if !linkCodeShape.MatchString(code) {
		return "", ErrUnknownLink
	}
	query := `
		SELECT v.content->>'link' FROM campaign_deliveries d
		JOIN campaign_versions v ON v.id = d.campaign_version_id
		WHERE d.link_code = $1`
	if count {
		query = `
			UPDATE campaign_deliveries d
			SET link_clicks = d.link_clicks + 1, link_clicked_at = COALESCE(d.link_clicked_at, now())
			FROM campaign_versions v
			WHERE d.link_code = $1 AND v.id = d.campaign_version_id
			RETURNING v.content->>'link'`
	}
	var link *string
	err := s.db.QueryRow(ctx, query, code).Scan(&link)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (link == nil || *link == "")) {
		return "", ErrUnknownLink
	}
	if err != nil {
		return "", fmt.Errorf("follow short link: %w", err)
	}
	return *link, nil
}
