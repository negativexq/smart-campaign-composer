-- +goose Up

-- A short code per SMS of a campaign with a link. The SMS carries https://url.short/<code>,
-- which leads to /api/l/<code> and on to the brief's link. The code is set when the
-- delivery is queued, so a retried message sends the same link.
ALTER TABLE campaign_deliveries
    ADD COLUMN link_code TEXT CHECK (link_code ~ '^[A-Za-z0-9_-]{8}$'),
    ADD COLUMN link_clicks INT NOT NULL DEFAULT 0,
    ADD COLUMN link_clicked_at TIMESTAMPTZ;

CREATE UNIQUE INDEX campaign_deliveries_link_code ON campaign_deliveries (link_code) WHERE link_code IS NOT NULL;

-- +goose Down

DROP INDEX campaign_deliveries_link_code;
ALTER TABLE campaign_deliveries
    DROP COLUMN link_clicked_at,
    DROP COLUMN link_clicks,
    DROP COLUMN link_code;
