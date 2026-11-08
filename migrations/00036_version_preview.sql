-- +goose Up

-- The start of each version's SMS and email subject, for the campaign list's rows.
-- Reading them out of content on every list request held a pooled connection long
-- enough to lower how many requests the API serves (docs/performance.md, step 8).
-- PostgreSQL fills them from content on every write, so they cannot fall behind.
ALTER TABLE campaign_versions
    ADD COLUMN preview_sms TEXT GENERATED ALWAYS AS (left(COALESCE(content->>'sms', ''), 160)) STORED,
    ADD COLUMN preview_email_subject TEXT GENERATED ALWAYS AS (left(COALESCE(content->>'email_subject', ''), 160)) STORED;

-- +goose Down

ALTER TABLE campaign_versions
    DROP COLUMN preview_email_subject,
    DROP COLUMN preview_sms;
