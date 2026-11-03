-- +goose Up
-- What the audience looked like when a person approved it, since consents and
-- events keep changing afterwards.
ALTER TABLE campaign_versions ADD COLUMN audience_snapshot JSONB;

-- +goose Down
ALTER TABLE campaign_versions DROP COLUMN audience_snapshot;
