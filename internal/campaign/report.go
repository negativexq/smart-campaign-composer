package campaign

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// maxTimelineBuckets keeps the chart readable however long a campaign has run.
const maxTimelineBuckets = 60

// timelineWidths are the bucket sizes people read easily, in seconds.
var timelineWidths = []int64{1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 21600, 43200, 86400}

// ReportReason says why deliveries ended without being accepted.
type ReportReason struct {
	Reason     string `json:"reason"`
	Deliveries int    `json:"deliveries"`
}

// ReportProblem counts one kind of unsuccessful provider call. A delivery can be
// listed here and still be accepted later.
type ReportProblem struct {
	Outcome    string `json:"outcome"`
	Deliveries int    `json:"deliveries"`
	Attempts   int    `json:"attempts"`
}

// TimelineBucket counts provider calls that started in one period.
type TimelineBucket struct {
	At          time.Time `json:"at"`
	Accepted    int       `json:"accepted"`
	RateLimited int       `json:"rate_limited"`
	ServerError int       `json:"server_error"`
	Rejected    int       `json:"rejected"`
	Timeout     int       `json:"timeout"`
}

// Timeline has a bucket for every period from the first call to the last, so a
// quiet period shows as a gap rather than disappearing.
type Timeline struct {
	BucketSeconds int64            `json:"bucket_seconds"`
	Buckets       []TimelineBucket `json:"buckets"`
}

// ReportEngagement counts accepted messages on one channel and how many of them
// the provider later reported opened.
type ReportEngagement struct {
	Channel  string `json:"channel"`
	Accepted int    `json:"accepted"`
	Opened   int    `json:"opened"`
}

// ReportLifecycle follows one channel's messages from who was planned to what the
// provider says became of them. Each step is part of the one before it, except that
// bounced and waiting together with delivered make up accepted.
type ReportLifecycle struct {
	Channel   string `json:"channel"`
	Planned   int    `json:"planned"`
	Eligible  int    `json:"eligible"`
	Called    int    `json:"called"`
	Accepted  int    `json:"accepted"`
	Delivered int    `json:"delivered"`
	Bounced   int    `json:"bounced"`
	Waiting   int    `json:"waiting"`
	// ArrivedUnknown had no answer to any call, and the provider later reported them
	// arrived. Their status stays UNKNOWN because our calls did get no answer.
	ArrivedUnknown int `json:"arrived_unknown"`
}

// ReportVariant follows one channel's messages for one version of a test. It compares
// what happened and names no winner, because nothing here measures what people did.
type ReportVariant struct {
	Variant   string `json:"variant"`
	Channel   string `json:"channel"`
	Planned   int    `json:"planned"`
	Accepted  int    `json:"accepted"`
	Delivered int    `json:"delivered"`
	Bounced   int    `json:"bounced"`
	Waiting   int    `json:"waiting"`
	Opened    int    `json:"opened"`
}

// Report is what the delivery records say about a campaign, beyond its counts.
type Report struct {
	Reasons    []ReportReason     `json:"reasons"`
	Problems   []ReportProblem    `json:"problems"`
	Timeline   Timeline           `json:"timeline"`
	Engagement []ReportEngagement `json:"engagement"`
	Lifecycle  []ReportLifecycle  `json:"lifecycle"`
	Variants   []ReportVariant    `json:"variants"`
}

var deliveryEvents = []string{"delivered", "bounced", "opened"}

// RecordDeliveryEvent stores what a provider reported about a delivery, found by
// its idempotency key because a delivery with a lost response has no message ID.
// Only the first report counts, since webhooks repeat.
func (s *Service) RecordDeliveryEvent(ctx context.Context, key, event string, occurredAt time.Time) error {
	if !slices.Contains(deliveryEvents, event) {
		return ErrUnknownEvent
	}
	var deliveryID, campaignID string
	err := s.db.QueryRow(ctx, `SELECT id::text, campaign_id::text FROM campaign_deliveries WHERE idempotency_key = $1`, key).Scan(&deliveryID, &campaignID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find delivery for event: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO delivery_events (delivery_id, campaign_id, event_type, occurred_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (delivery_id, event_type) DO NOTHING`, deliveryID, campaignID, event, occurredAt); err != nil {
		return fmt.Errorf("record delivery event: %w", err)
	}
	return nil
}

// Report reads a campaign's delivery history.
func (s *Service) Report(ctx context.Context, id string) (Report, error) {
	if _, err := s.load(ctx, id); err != nil {
		return Report{}, err
	}
	reasons, err := s.reportReasons(ctx, id)
	if err != nil {
		return Report{}, err
	}
	problems, err := s.reportProblems(ctx, id)
	if err != nil {
		return Report{}, err
	}
	timeline, err := s.reportTimeline(ctx, id)
	if err != nil {
		return Report{}, err
	}
	engagement, err := s.reportEngagement(ctx, id)
	if err != nil {
		return Report{}, err
	}
	lifecycle, err := s.reportLifecycle(ctx, id)
	if err != nil {
		return Report{}, err
	}
	variants, err := s.reportVariants(ctx, id)
	if err != nil {
		return Report{}, err
	}
	return Report{Reasons: reasons, Problems: problems, Timeline: timeline, Engagement: engagement, Lifecycle: lifecycle, Variants: variants}, nil
}

// arrivalLateral reads one delivery's arrival. An open proves arrival even if the
// delivered report was lost, and a bounce is the more specific news, so it wins.
const arrivalLateral = `LEFT JOIN LATERAL (
		SELECT max(occurred_at) FILTER (WHERE event_type = 'bounced') AS bounced_at,
		       CASE WHEN count(*) FILTER (WHERE event_type = 'bounced') = 0
		            THEN min(occurred_at) FILTER (WHERE event_type IN ('delivered', 'opened')) END AS delivered_at
		FROM delivery_events WHERE delivery_id = d.id
	) arrival ON true`

func (s *Service) reportLifecycle(ctx context.Context, id string) ([]ReportLifecycle, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.channel, count(*),
		       count(*) FILTER (WHERE d.status <> 'SKIPPED'),
		       count(*) FILTER (WHERE EXISTS (SELECT 1 FROM delivery_attempts a WHERE a.delivery_id = d.id)),
		       count(*) FILTER (WHERE d.status = 'SENT'),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.bounced_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NULL AND arrival.bounced_at IS NULL),
		       count(*) FILTER (WHERE d.status = 'UNKNOWN' AND arrival.delivered_at IS NOT NULL)
		FROM campaign_deliveries d
		`+arrivalLateral+`
		WHERE d.campaign_id = $1
		GROUP BY d.channel
		ORDER BY d.channel DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("read report lifecycle: %w", err)
	}
	lifecycle, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReportLifecycle])
	if err != nil {
		return nil, fmt.Errorf("read report lifecycle: %w", err)
	}
	return lifecycle, nil
}

func (s *Service) reportVariants(ctx context.Context, id string) ([]ReportVariant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.variant, d.channel, count(*),
		       count(*) FILTER (WHERE d.status = 'SENT'),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.bounced_at IS NOT NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND arrival.delivered_at IS NULL AND arrival.bounced_at IS NULL),
		       count(*) FILTER (WHERE d.status = 'SENT' AND EXISTS (
		           SELECT 1 FROM delivery_events e WHERE e.delivery_id = d.id AND e.event_type = 'opened'))
		FROM campaign_deliveries d
		`+arrivalLateral+`
		WHERE d.campaign_id = $1 AND d.variant IS NOT NULL
		GROUP BY d.variant, d.channel
		ORDER BY d.channel DESC, d.variant`, id)
	if err != nil {
		return nil, fmt.Errorf("read report variants: %w", err)
	}
	variants, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReportVariant])
	if err != nil {
		return nil, fmt.Errorf("read report variants: %w", err)
	}
	return variants, nil
}

func (s *Service) reportEngagement(ctx context.Context, id string) ([]ReportEngagement, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.channel, count(*), count(e.delivery_id)
		FROM campaign_deliveries d
		LEFT JOIN delivery_events e ON e.delivery_id = d.id AND e.event_type = 'opened'
		WHERE d.campaign_id = $1 AND d.status = 'SENT'
		GROUP BY d.channel
		ORDER BY d.channel DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("read report engagement: %w", err)
	}
	engagement, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReportEngagement])
	if err != nil {
		return nil, fmt.Errorf("read report engagement: %w", err)
	}
	return engagement, nil
}

func (s *Service) reportReasons(ctx context.Context, id string) ([]ReportReason, error) {
	rows, err := s.db.Query(ctx, `
		SELECT last_error, count(*)
		FROM campaign_deliveries
		WHERE campaign_id = $1 AND status IN ('FAILED', 'UNKNOWN', 'SKIPPED') AND last_error IS NOT NULL
		GROUP BY last_error
		ORDER BY count(*) DESC, last_error`, id)
	if err != nil {
		return nil, fmt.Errorf("read report reasons: %w", err)
	}
	reasons, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReportReason])
	if err != nil {
		return nil, fmt.Errorf("read report reasons: %w", err)
	}
	return reasons, nil
}

func (s *Service) reportProblems(ctx context.Context, id string) ([]ReportProblem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT outcome, count(DISTINCT delivery_id), count(*)
		FROM delivery_attempts
		WHERE campaign_id = $1 AND outcome <> 'accepted'
		GROUP BY outcome
		ORDER BY count(*) DESC, outcome`, id)
	if err != nil {
		return nil, fmt.Errorf("read report problems: %w", err)
	}
	problems, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ReportProblem])
	if err != nil {
		return nil, fmt.Errorf("read report problems: %w", err)
	}
	return problems, nil
}

func (s *Service) reportTimeline(ctx context.Context, id string) (Timeline, error) {
	timeline := Timeline{Buckets: []TimelineBucket{}}
	var first, last *time.Time
	if err := s.db.QueryRow(ctx, `SELECT min(occurred_at), max(occurred_at) FROM delivery_attempts WHERE campaign_id = $1`, id).Scan(&first, &last); err != nil {
		return timeline, fmt.Errorf("read report timeline: %w", err)
	}
	if first == nil || last == nil {
		return timeline, nil
	}
	timeline.BucketSeconds = bucketWidth(*first, *last)

	rows, err := s.db.Query(ctx, `
		SELECT date_bin(make_interval(secs => $2), occurred_at, 'epoch'::timestamptz),
		       count(*) FILTER (WHERE outcome = 'accepted'),
		       count(*) FILTER (WHERE outcome = 'rate_limited'),
		       count(*) FILTER (WHERE outcome = 'server_error'),
		       count(*) FILTER (WHERE outcome = 'rejected'),
		       count(*) FILTER (WHERE outcome = 'timeout')
		FROM delivery_attempts
		WHERE campaign_id = $1
		GROUP BY 1`, id, float64(timeline.BucketSeconds))
	if err != nil {
		return timeline, fmt.Errorf("read report timeline: %w", err)
	}
	filled, err := pgx.CollectRows(rows, pgx.RowToStructByPos[TimelineBucket])
	if err != nil {
		return timeline, fmt.Errorf("read report timeline: %w", err)
	}
	byStart := make(map[int64]TimelineBucket, len(filled))
	for _, bucket := range filled {
		byStart[bucket.At.Unix()] = bucket
	}
	end := bucketStart(*last, timeline.BucketSeconds)
	for start := bucketStart(*first, timeline.BucketSeconds); start <= end; start += timeline.BucketSeconds {
		bucket, found := byStart[start]
		if !found {
			bucket = TimelineBucket{At: time.Unix(start, 0).UTC()}
		}
		timeline.Buckets = append(timeline.Buckets, bucket)
	}
	return timeline, nil
}

func bucketStart(at time.Time, width int64) int64 {
	return at.Unix() - at.Unix()%width
}

// bucketWidth picks the first readable size that keeps the chart small.
func bucketWidth(first, last time.Time) int64 {
	span := last.Unix() - first.Unix()
	for _, width := range timelineWidths {
		if span/width < maxTimelineBuckets {
			return width
		}
	}
	return span/maxTimelineBuckets + 1
}
