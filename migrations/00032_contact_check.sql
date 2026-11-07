-- +goose Up

-- Before a message with a "not messaged recently" rule goes out, the worker looks for
-- the person's messages that are being sent or are waiting to be tried again. Few rows
-- are in those states at a time, so this index stays small.
CREATE INDEX campaign_deliveries_open_contact_idx ON campaign_deliveries (user_id)
    WHERE status IN ('SENDING', 'RETRYING');

-- +goose Down

DROP INDEX campaign_deliveries_open_contact_idx;
