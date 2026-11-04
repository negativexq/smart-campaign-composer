-- +goose Up
-- Languages a campaign does not write in. Their readers stay in the audience and get English.
ALTER TABLE campaigns ADD COLUMN excluded_languages TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE campaigns DROP COLUMN excluded_languages;
