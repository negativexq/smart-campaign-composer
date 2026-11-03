-- +goose Up

-- Adding the channel lets the campaign list count from the index alone.
CREATE INDEX campaign_deliveries_status_channel_idx ON campaign_deliveries (campaign_id, status, channel);
DROP INDEX campaign_deliveries_progress_idx;

-- Live progress counts provider responses every second. Reaching them through the
-- deliveries took about 3 seconds for a campaign of a million messages, and this
-- takes about 60 ms.
ALTER TABLE delivery_attempts ADD COLUMN campaign_id UUID REFERENCES campaigns (id) ON DELETE CASCADE;
UPDATE delivery_attempts a SET campaign_id = d.campaign_id FROM campaign_deliveries d WHERE d.id = a.delivery_id;
ALTER TABLE delivery_attempts ALTER COLUMN campaign_id SET NOT NULL;
CREATE INDEX delivery_attempts_campaign_response_idx ON delivery_attempts (campaign_id, response_code);

-- +goose Down

DROP INDEX delivery_attempts_campaign_response_idx;
ALTER TABLE delivery_attempts DROP COLUMN campaign_id;
CREATE INDEX campaign_deliveries_progress_idx ON campaign_deliveries (campaign_id, status);
DROP INDEX campaign_deliveries_status_channel_idx;
