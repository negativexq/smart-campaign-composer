-- +goose Up

-- When a campaign entered its status. Waiting on a stalled send and the channel
-- alerts are timed from this, not from updated_at, which a rename or a new tag also
-- moves: renaming a stalled campaign every few minutes would otherwise keep it from
-- ever pausing. A trigger keeps it right for every change of status.
ALTER TABLE campaigns ADD COLUMN status_since TIMESTAMPTZ NOT NULL DEFAULT now();
UPDATE campaigns SET status_since = updated_at;

-- +goose StatementBegin
CREATE FUNCTION mark_campaign_status_since() RETURNS trigger AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        NEW.status_since = now();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER campaigns_status_since
    BEFORE UPDATE OF status ON campaigns
    FOR EACH ROW EXECUTE FUNCTION mark_campaign_status_since();

-- +goose Down

DROP TRIGGER campaigns_status_since ON campaigns;
DROP FUNCTION mark_campaign_status_since();
ALTER TABLE campaigns DROP COLUMN status_since;
