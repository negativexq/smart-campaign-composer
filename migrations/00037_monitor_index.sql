-- +goose Up

-- The delivery monitor reads only deliveries still on their way or ended badly. They
-- are a few thousand rows at most while sending, but the table keeps every message
-- ever sent, so without this index each of the monitor's counts read all of it
-- (docs/performance.md, step 10). The monitor's query names these statuses itself, so
-- every plan may use the index. Rows enter it while queued and leave it once sent or
-- skipped, so it stays small; its cost is one more index write per status change of
-- a message still sending.
CREATE INDEX campaign_deliveries_monitor_idx ON campaign_deliveries (updated_at DESC, id)
    WHERE status IN ('QUEUED', 'SENDING', 'RETRYING', 'UNKNOWN', 'FAILED');

-- +goose Down

DROP INDEX campaign_deliveries_monitor_idx;
