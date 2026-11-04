-- +goose Up

-- A campaign can send two versions of its messages to compare them.
ALTER TABLE campaigns ADD COLUMN ab_test BOOLEAN NOT NULL DEFAULT false;
-- The share of people who get version A; the rest get version B.
ALTER TABLE campaigns ADD COLUMN variant_a_percent INTEGER NOT NULL DEFAULT 50 CHECK (variant_a_percent BETWEEN 1 AND 99);
-- Which version a person got. Empty for a campaign with one version.
ALTER TABLE campaign_deliveries ADD COLUMN variant TEXT CHECK (variant IN ('A', 'B'));

-- +goose Down

ALTER TABLE campaign_deliveries DROP COLUMN variant;
ALTER TABLE campaigns DROP COLUMN variant_a_percent;
ALTER TABLE campaigns DROP COLUMN ab_test;
