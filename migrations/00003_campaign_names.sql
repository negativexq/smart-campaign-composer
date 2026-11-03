-- +goose Up

-- name_source records who chose the name, so a name someone typed is never
-- replaced by a later suggestion: brief (derived), ai (suggested) or user.
ALTER TABLE campaigns
    ADD COLUMN name TEXT,
    ADD COLUMN name_source TEXT NOT NULL DEFAULT 'brief' CHECK (name_source IN ('brief', 'ai', 'user'));

UPDATE campaigns SET name = left(trim(regexp_replace(brief, '\s+', ' ', 'g')), 60);

ALTER TABLE campaigns ALTER COLUMN name SET NOT NULL;

-- +goose Down

ALTER TABLE campaigns DROP COLUMN name_source, DROP COLUMN name;
