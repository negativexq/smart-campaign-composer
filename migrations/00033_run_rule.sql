-- +goose Up

-- A run keeps the "not messaged recently" rule it was queued with and the time
-- queuing counted messages up to. The worker checks again before sending only when
-- another run has been open since then, so a campaign sending on its own does no
-- extra work.
ALTER TABLE campaign_runs ADD COLUMN rule_days INT, ADD COLUMN rule_as_of TIMESTAMPTZ;

-- Runs still sending keep checking until they finish: when queuing counted is not known.
UPDATE campaign_runs r
SET rule_days = (
        SELECT (v.segment #>> '{segment,not_messaged_within_days}')::int
        FROM campaign_deliveries d JOIN campaign_versions v ON v.id = d.campaign_version_id
        WHERE d.run_id = r.id LIMIT 1),
    rule_as_of = '-infinity'
WHERE r.finished_at IS NULL;

CREATE INDEX campaign_runs_finished_idx ON campaign_runs (finished_at);

-- +goose Down

DROP INDEX campaign_runs_finished_idx;
ALTER TABLE campaign_runs DROP COLUMN rule_days, DROP COLUMN rule_as_of;
