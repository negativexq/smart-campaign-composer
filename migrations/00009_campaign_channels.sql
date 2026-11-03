-- +goose Up

-- Chosen when a campaign is created. Existing campaigns used both.
ALTER TABLE campaigns ADD COLUMN channels TEXT[] NOT NULL DEFAULT '{sms,email}'
    CHECK (cardinality(channels) > 0 AND channels <@ ARRAY['sms', 'email']);

-- +goose Down

ALTER TABLE campaigns DROP COLUMN channels;
