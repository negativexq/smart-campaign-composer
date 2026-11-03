-- +goose Up

-- A 429 is logged but does not use up an attempt, so one attempt number can have
-- several rows.
ALTER TABLE delivery_attempts DROP CONSTRAINT delivery_attempts_delivery_id_attempt_key;
CREATE INDEX delivery_attempts_delivery_idx ON delivery_attempts (delivery_id, id);

-- Lets a send's trace link back to the dispatch that queued it.
ALTER TABLE campaign_deliveries ADD COLUMN trace_context TEXT;

-- +goose Down

ALTER TABLE campaign_deliveries DROP COLUMN trace_context;
DROP INDEX delivery_attempts_delivery_idx;
ALTER TABLE delivery_attempts ADD CONSTRAINT delivery_attempts_delivery_id_attempt_key UNIQUE (delivery_id, attempt);
