-- +goose Up

-- The overview counts recent calls per channel. Joining to the deliveries scanned
-- all of them on every request, so it slowed down as history grew. With the channel
-- on the attempt, the cost depends only on the calls in the window, which the rate
-- limit bounds.
ALTER TABLE delivery_attempts ADD COLUMN channel TEXT CHECK (channel IN ('sms', 'email'));
UPDATE delivery_attempts a SET channel = d.channel FROM campaign_deliveries d WHERE d.id = a.delivery_id;
ALTER TABLE delivery_attempts ALTER COLUMN channel SET NOT NULL;
CREATE INDEX delivery_attempts_channel_occurred_idx ON delivery_attempts (channel, occurred_at DESC) INCLUDE (outcome);

-- The index on occurred_at alone made the planner walk every campaign's calls to
-- find one campaign's first and last. This one goes straight to them.
CREATE INDEX delivery_attempts_campaign_occurred_idx ON delivery_attempts (campaign_id, occurred_at);
DROP INDEX delivery_attempts_occurred_idx;

-- +goose Down

CREATE INDEX delivery_attempts_occurred_idx ON delivery_attempts (occurred_at DESC);
DROP INDEX delivery_attempts_campaign_occurred_idx;
DROP INDEX delivery_attempts_channel_occurred_idx;
ALTER TABLE delivery_attempts DROP COLUMN channel;
