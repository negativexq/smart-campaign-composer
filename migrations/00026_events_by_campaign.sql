-- +goose Up

-- A campaign's report reads all of its delivery events at once. Without this it
-- scanned the events of every campaign.
CREATE INDEX delivery_events_campaign_idx ON delivery_events (campaign_id);

-- +goose Down

DROP INDEX delivery_events_campaign_idx;
