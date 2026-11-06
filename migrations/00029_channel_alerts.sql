-- +goose Up

-- Workers look up which channel alerts are open every 30 seconds. Alerts are rare,
-- so this keeps that from reading every audit event.
CREATE INDEX audit_events_alerts_idx ON audit_events (campaign_id, id)
    WHERE event_type IN ('campaign.alert_opened', 'campaign.alert_closed');

-- +goose Down

DROP INDEX audit_events_alerts_idx;
