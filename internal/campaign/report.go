package campaign

import (
	"context"
	"fmt"
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

// Report is what the delivery records say about a campaign, beyond its counts.
type Report struct {
	Reasons  []ReportReason  `json:"reasons"`
	Problems []ReportProblem `json:"problems"`
	Timeline Timeline        `json:"timeline"`
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
	return Report{Reasons: reasons, Problems: problems, Timeline: timeline}, nil
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
