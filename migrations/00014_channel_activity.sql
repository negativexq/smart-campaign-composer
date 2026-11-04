-- +goose Up

-- The overview reads the newest provider calls across all campaigns.
CREATE INDEX delivery_attempts_occurred_idx ON delivery_attempts (occurred_at DESC);

-- +goose Down

DROP INDEX delivery_attempts_occurred_idx;
