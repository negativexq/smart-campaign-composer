-- +goose Up
-- A rule kept for reuse, apart from any campaign. Its counts are the last time someone
-- asked, unlike an approved campaign's audience, which never changes.
CREATE TABLE saved_segments (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT NOT NULL,
    spec               JSONB NOT NULL,
    source_campaign_id UUID REFERENCES campaigns (id) ON DELETE SET NULL,
    created_by         TEXT,
    total              INT NOT NULL,
    sms                INT NOT NULL,
    email              INT NOT NULL,
    counted_at         TIMESTAMPTZ NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX saved_segments_name_idx ON saved_segments (lower(name));

-- +goose Down
DROP TABLE saved_segments;
