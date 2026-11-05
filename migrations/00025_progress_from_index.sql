-- +goose Up

-- Progress also sums attempts, which made it read every delivery of the campaign from
-- the table. Carrying attempts in the index lets it count from the index, like the
-- campaign list. Attempts change together with the status, which is already indexed,
-- so updates cost the same.
CREATE INDEX campaign_deliveries_progress_idx ON campaign_deliveries (campaign_id, status, channel) INCLUDE (attempts);
DROP INDEX campaign_deliveries_status_channel_idx;

-- +goose Down

CREATE INDEX campaign_deliveries_status_channel_idx ON campaign_deliveries (campaign_id, status, channel);
DROP INDEX campaign_deliveries_progress_idx;
