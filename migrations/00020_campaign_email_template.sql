-- +goose Up
-- The design chosen before any message exists. Once written, a message carries its own.
ALTER TABLE campaigns ADD COLUMN email_template TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE campaigns DROP COLUMN email_template;
