package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// A channel alert tells the people running a campaign that one of its channels is in
// trouble while the campaign keeps going. Sending is never stopped by one: the breaker
// and the credential lock already keep messages from using up their attempts, and a
// provider that recovers carries on at once. Each kind opens and closes on its own,
// so one clearing never hides another still open.
const (
	// alertErrorRate: most of the channel's last calls failed. It counts calls, not a
	// span of time, so a healthy stretch before the provider broke, from this campaign
	// or another, does not hide the failures that follow it.
	alertErrorRate = "error_rate"
	// alertNoProgress: retries wait on the channel and none of its calls was accepted
	// over the alert span. It covers what leaves no call behind, such as a provider
	// that cannot be reached, while another channel keeps the campaign moving.
	alertNoProgress = "no_progress"
	// alertCredentials: the provider refused the channel's credentials.
	alertCredentials = "credentials_refused"

	eventAlertOpened = "campaign.alert_opened"
	eventAlertClosed = "campaign.alert_closed"

	defaultAlertAfter = 10 * time.Minute
	// alertMinCalls keeps a few calls from deciding a rate either way. The error rate
	// is also read over this many of the channel's last calls.
	alertMinCalls = 20
)

// alertKinds is every kind, in the order they are looked at.
var alertKinds = []string{alertCredentials, alertNoProgress, alertErrorRate}

// channelSignals is what the channel's calls show, over the alert span and over the
// last tenth of it, which is how recovery is judged: one call that got through
// after a bad patch does not clear an alert.
type channelSignals struct {
	calls, failed, accepted                   int
	recentCalls, recentFailed, recentAccepted int
	// lastCalls and lastFailed count the channel's last alertMinCalls calls within
	// the span.
	lastCalls, lastFailed int
	// locked is whether the credentials are refused; known is false when Redis
	// could not say, and then that alert is left as it is.
	locked, known bool
}

// campaignChannel is one sending campaign's work on one channel.
type campaignChannel struct {
	campaign, channel string
	sending           bool
	// longRunning means it has been sending, without a restart, for the alert span.
	longRunning      bool
	waiting, retries bool
}

// CheckAlerts opens and closes channel alerts on every campaign.
func (w *Worker) CheckAlerts(ctx context.Context) error {
	return w.checkAlerts(ctx, channels)
}

func (w *Worker) checkAlerts(ctx context.Context, onChannels []string) error {
	if w.config.AlertAfter <= 0 {
		return nil
	}
	span := w.config.AlertAfter
	signals, err := w.readSignals(ctx, onChannels, span)
	if err != nil {
		return err
	}
	open, err := w.openAlerts(ctx)
	if err != nil {
		return err
	}
	withOpen := make([]string, 0, len(open))
	for key := range open {
		withOpen = append(withOpen, key.campaign)
	}
	rows, err := w.db.Query(ctx, `
		SELECT c.id::text, ch.channel, c.status = 'DISPATCHING', c.updated_at < now() - make_interval(secs => $1),
		       EXISTS (SELECT 1 FROM campaign_deliveries d WHERE d.campaign_id = c.id AND d.channel = ch.channel AND d.status IN ('QUEUED', 'RETRYING', 'SENDING')),
		       EXISTS (SELECT 1 FROM campaign_deliveries d WHERE d.campaign_id = c.id AND d.channel = ch.channel AND d.status = 'RETRYING')
		FROM campaigns c CROSS JOIN unnest($2::text[]) AS ch(channel)
		WHERE c.deleted_at IS NULL AND (c.status = 'DISPATCHING' OR c.id = ANY($3::uuid[]))`,
		span.Seconds(), onChannels, withOpen)
	if err != nil {
		return fmt.Errorf("read campaigns for alerts: %w", err)
	}
	work, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (campaignChannel, error) {
		var c campaignChannel
		err := row.Scan(&c.campaign, &c.channel, &c.sending, &c.longRunning, &c.waiting, &c.retries)
		return c, err
	})
	if err != nil {
		return fmt.Errorf("read campaigns for alerts: %w", err)
	}
	var failures []error
	for _, c := range work {
		s := signals[c.channel]
		for _, kind := range alertKinds {
			key := alertKey{c.campaign, c.channel, kind}
			opens, closes, details := decideAlert(kind, c, s, span)
			switch {
			case !open[key] && opens:
				failures = append(failures, w.setAlert(ctx, key, true, details))
			case open[key] && closes:
				failures = append(failures, w.setAlert(ctx, key, false, nil))
			}
		}
	}
	return errors.Join(failures...)
}

// decideAlert says whether an alert of kind should open, and whether one open should
// close. Neither leaves it as it is, so it cannot flap on a borderline reading.
func decideAlert(kind string, c campaignChannel, s channelSignals, span time.Duration) (opens, closes bool, details map[string]any) {
	switch kind {
	case alertCredentials:
		if !s.known {
			return false, false, nil
		}
		return c.sending && c.waiting && s.locked, !c.waiting || !s.locked, nil
	case alertNoProgress:
		opens = c.sending && c.longRunning && c.retries && s.accepted == 0
		closes = !c.waiting || s.recentAccepted >= alertMinCalls
		return opens, closes, map[string]any{"minutes": int(span.Minutes())}
	case alertErrorRate:
		opens = c.sending && c.waiting && s.lastCalls >= alertMinCalls && 2*s.lastFailed >= s.lastCalls
		closes = !c.waiting || (s.recentCalls >= alertMinCalls && 2*s.recentFailed < s.recentCalls)
		return opens, closes, map[string]any{"calls": s.lastCalls, "failed": s.lastFailed}
	}
	return false, false, nil
}

// readSignals reads every channel's calls in one pass over the recent end of an
// index that carries the outcome, and asks Redis whether each channel is locked.
func (w *Worker) readSignals(ctx context.Context, onChannels []string, span time.Duration) (map[string]channelSignals, error) {
	rows, err := w.db.Query(ctx, `
		SELECT channel, count(*),
		       count(*) FILTER (WHERE outcome IN ('server_error', 'timeout')),
		       count(*) FILTER (WHERE outcome = 'accepted'),
		       count(*) FILTER (WHERE occurred_at >= now() - make_interval(secs => $2)),
		       count(*) FILTER (WHERE occurred_at >= now() - make_interval(secs => $2) AND outcome IN ('server_error', 'timeout')),
		       count(*) FILTER (WHERE occurred_at >= now() - make_interval(secs => $2) AND outcome = 'accepted')
		FROM delivery_attempts
		WHERE channel = ANY($3::text[]) AND occurred_at >= now() - make_interval(secs => $1)
		GROUP BY channel`, span.Seconds(), (span / 10).Seconds(), onChannels)
	if err != nil {
		return nil, fmt.Errorf("read channel calls: %w", err)
	}
	signals := map[string]channelSignals{}
	for rows.Next() {
		var (
			channel string
			s       channelSignals
		)
		if err := rows.Scan(&channel, &s.calls, &s.failed, &s.accepted, &s.recentCalls, &s.recentFailed, &s.recentAccepted); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read channel calls: %w", err)
		}
		signals[channel] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read channel calls: %w", err)
	}
	// The last calls come from the end of the same index, a few rows per channel.
	rows, err = w.db.Query(ctx, `
		SELECT ch.channel, count(last.outcome), count(*) FILTER (WHERE last.outcome IN ('server_error', 'timeout'))
		FROM unnest($2::text[]) AS ch (channel)
		CROSS JOIN LATERAL (
			SELECT a.outcome FROM delivery_attempts a
			WHERE a.channel = ch.channel AND a.occurred_at >= now() - make_interval(secs => $1)
			ORDER BY a.occurred_at DESC LIMIT $3
		) last
		GROUP BY ch.channel`, span.Seconds(), onChannels, alertMinCalls)
	if err != nil {
		return nil, fmt.Errorf("read last channel calls: %w", err)
	}
	for rows.Next() {
		var (
			channel             string
			lastCalls, lastFail int
		)
		if err := rows.Scan(&channel, &lastCalls, &lastFail); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read last channel calls: %w", err)
		}
		s := signals[channel]
		s.lastCalls, s.lastFailed = lastCalls, lastFail
		signals[channel] = s
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read last channel calls: %w", err)
	}
	for _, channel := range onChannels {
		s := signals[channel]
		locked, err := w.limiter.CredentialsLocked(ctx, channel)
		if err != nil {
			slog.WarnContext(ctx, "read channel lock for alerts", "channel", channel, "error", err)
		} else {
			s.locked, s.known = locked, true
		}
		signals[channel] = s
	}
	return signals, nil
}

type alertKey struct{ campaign, channel, kind string }

// openAlerts reads which alerts are open: those whose latest event is an opening.
func (w *Worker) openAlerts(ctx context.Context) (map[alertKey]bool, error) {
	rows, err := w.db.Query(ctx, `
		SELECT campaign_id::text, channel, kind FROM (
			SELECT DISTINCT ON (campaign_id, details->>'channel', details->>'alert')
			       campaign_id, details->>'channel' AS channel, details->>'alert' AS kind, event_type
			FROM audit_events WHERE event_type IN ($1, $2)
			ORDER BY campaign_id, details->>'channel', details->>'alert', id DESC
		) latest WHERE event_type = $1`, eventAlertOpened, eventAlertClosed)
	if err != nil {
		return nil, fmt.Errorf("read open alerts: %w", err)
	}
	keys, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (alertKey, error) {
		var key alertKey
		err := row.Scan(&key.campaign, &key.channel, &key.kind)
		return key, err
	})
	if err != nil {
		return nil, fmt.Errorf("read open alerts: %w", err)
	}
	open := make(map[alertKey]bool, len(keys))
	for _, key := range keys {
		open[key] = true
	}
	return open, nil
}

// setAlert records an alert opening or closing, once: under a lock on that campaign,
// channel and kind, it looks at the latest event again, so workers checking at the
// same moment write it once between them.
func (w *Worker) setAlert(ctx context.Context, key alertKey, opening bool, details map[string]any) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin alert: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "alert:"+key.campaign+":"+key.channel+":"+key.kind); err != nil {
		return fmt.Errorf("lock alert: %w", err)
	}
	var isOpen bool
	err = tx.QueryRow(ctx, `
		SELECT event_type = $4 FROM audit_events
		WHERE campaign_id = $1 AND event_type IN ($4, $5) AND details->>'channel' = $2 AND details->>'alert' = $3
		ORDER BY id DESC LIMIT 1`, key.campaign, key.channel, key.kind, eventAlertOpened, eventAlertClosed).Scan(&isOpen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read alert: %w", err)
	}
	if isOpen == opening {
		return nil
	}
	event, state := eventAlertClosed, "closed"
	if opening {
		event, state = eventAlertOpened, "opened"
	}
	all := map[string]any{"channel": key.channel, "alert": key.kind}
	for name, value := range details {
		all[name] = value
	}
	encoded, err := json.Marshal(all)
	if err != nil {
		return fmt.Errorf("encode alert: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (campaign_id, event_type, details) VALUES ($1, $2, $3)`, key.campaign, event, encoded); err != nil {
		return fmt.Errorf("record alert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit alert: %w", err)
	}
	w.metrics.DeliveryAlerts.WithLabelValues(key.channel, key.kind, state).Inc()
	slog.WarnContext(ctx, "channel alert "+state, "campaign_id", key.campaign, "channel", key.channel, "alert", key.kind)
	return nil
}
