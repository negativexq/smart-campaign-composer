-- +goose Up

-- Choices made on the form rather than left to the brief.
ALTER TABLE campaigns
    ADD COLUMN tone TEXT NOT NULL DEFAULT '' CHECK (tone IN ('', 'friendly', 'formal', 'urgent')),
    ADD COLUMN offer_percent INT CHECK (offer_percent BETWEEN 1 AND 100),
    ADD COLUMN not_messaged_within_days INT CHECK (not_messaged_within_days BETWEEN 1 AND 365);

-- Audience counts look up who was messaged recently, per person.
CREATE INDEX campaign_deliveries_recent_idx ON campaign_deliveries (user_id, updated_at)
    WHERE status IN ('SENT', 'UNKNOWN');

-- +goose Down

DROP INDEX campaign_deliveries_recent_idx;
ALTER TABLE campaigns
    DROP COLUMN tone,
    DROP COLUMN offer_percent,
    DROP COLUMN not_messaged_within_days;
