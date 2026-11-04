-- +goose Up

-- Each channel claims its own deliveries. Without the channel in the index, a channel
-- with nothing due would read past everything the other one has waiting.
CREATE INDEX campaign_deliveries_channel_claimable_idx ON campaign_deliveries (channel, next_attempt_at)
    WHERE status IN ('QUEUED', 'RETRYING');
DROP INDEX campaign_deliveries_claimable_idx;

-- +goose Down

CREATE INDEX campaign_deliveries_claimable_idx ON campaign_deliveries (next_attempt_at)
    WHERE status IN ('QUEUED', 'RETRYING');
DROP INDEX campaign_deliveries_channel_claimable_idx;
