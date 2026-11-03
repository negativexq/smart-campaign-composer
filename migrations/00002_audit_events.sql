-- +goose Up

-- The durable answer to "what happened to this campaign and on whose say-so".
-- It must outlive any telemetry retention, so it lives with the business data.
CREATE TABLE audit_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    campaign_id UUID NOT NULL REFERENCES campaigns (id),
    event_type  TEXT NOT NULL,
    details     JSONB NOT NULL DEFAULT '{}',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_campaign_idx ON audit_events (campaign_id, id);

-- +goose StatementBegin
CREATE FUNCTION reject_audit_event_change() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit events are append-only';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION reject_audit_event_change();

-- +goose Down

DROP TRIGGER audit_events_append_only ON audit_events;
DROP FUNCTION reject_audit_event_change();
DROP TABLE audit_events;
