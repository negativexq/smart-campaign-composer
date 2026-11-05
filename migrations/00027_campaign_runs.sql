-- +goose Up

-- Each send of a campaign is a run: the one dispatch of a one-time campaign, or one
-- firing of a repeating schedule. A person gets at most one message per channel in a
-- run, and a finished run keeps its counts, so reading a campaign's totals does not
-- recount every delivery it ever made.
CREATE TABLE campaign_runs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    number      INT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    queued      INT NOT NULL DEFAULT 0,
    skipped     INT NOT NULL DEFAULT 0,
    -- Set together once nothing in the run can change any more.
    finished_at TIMESTAMPTZ,
    total       INT,
    counts      JSONB, -- [[channel, status, deliveries, attempts], ...]
    responses   JSONB, -- {"<HTTP code>": calls, ...}
    UNIQUE (campaign_id, number),
    CHECK ((finished_at IS NULL) = (counts IS NULL) AND (counts IS NULL) = (total IS NULL) AND (total IS NULL) = (responses IS NULL))
);
CREATE INDEX campaign_runs_open_idx ON campaign_runs (campaign_id) WHERE finished_at IS NULL;

-- What was sent before runs existed becomes each campaign's first run.
INSERT INTO campaign_runs (campaign_id, number, started_at, queued, skipped)
SELECT campaign_id, 1, min(created_at),
       count(*) FILTER (WHERE status <> 'SKIPPED'), count(*) FILTER (WHERE status = 'SKIPPED')
FROM campaign_deliveries GROUP BY campaign_id;

ALTER TABLE campaign_deliveries ADD COLUMN run_id UUID REFERENCES campaign_runs (id) ON DELETE CASCADE;
UPDATE campaign_deliveries d SET run_id = r.id FROM campaign_runs r WHERE r.campaign_id = d.campaign_id;
ALTER TABLE campaign_deliveries ALTER COLUMN run_id SET NOT NULL;

ALTER TABLE delivery_attempts ADD COLUMN run_id UUID REFERENCES campaign_runs (id) ON DELETE CASCADE;
UPDATE delivery_attempts a SET run_id = d.run_id FROM campaign_deliveries d WHERE d.id = a.delivery_id;
ALTER TABLE delivery_attempts ALTER COLUMN run_id SET NOT NULL;
CREATE INDEX delivery_attempts_run_response_idx ON delivery_attempts (run_id, response_code);

-- One message per person and channel is now a promise of each run. The campaign-wide
-- index stays for finding who an earlier run already reached.
ALTER TABLE campaign_deliveries DROP CONSTRAINT campaign_deliveries_campaign_id_user_id_channel_key;
ALTER TABLE campaign_deliveries ADD CONSTRAINT campaign_deliveries_run_id_user_id_channel_key UNIQUE (run_id, user_id, channel);
CREATE INDEX campaign_deliveries_reached_idx ON campaign_deliveries (campaign_id, user_id, channel);
CREATE INDEX campaign_deliveries_run_progress_idx ON campaign_deliveries (run_id, status, channel) INCLUDE (attempts);

-- Runs with nothing left to send are finished already.
UPDATE campaign_runs r
SET finished_at = COALESCE((SELECT max(d.updated_at) FROM campaign_deliveries d WHERE d.run_id = r.id), r.started_at),
    total = (SELECT count(*) FROM campaign_deliveries d WHERE d.run_id = r.id),
    counts = COALESCE((
        SELECT jsonb_agg(jsonb_build_array(s.channel, s.status, s.deliveries, s.attempts) ORDER BY s.channel, s.status)
        FROM (SELECT channel, status, count(*) AS deliveries, sum(attempts) AS attempts
              FROM campaign_deliveries WHERE run_id = r.id GROUP BY channel, status) s), '[]'),
    responses = COALESCE((
        SELECT jsonb_object_agg(s.code, s.calls)
        FROM (SELECT response_code::text AS code, count(*) AS calls
              FROM delivery_attempts WHERE run_id = r.id AND response_code IS NOT NULL GROUP BY response_code) s), '{}')
WHERE NOT EXISTS (
    SELECT 1 FROM campaign_deliveries d WHERE d.run_id = r.id AND d.status IN ('QUEUED', 'SENDING', 'RETRYING')
);

-- +goose Down

DROP INDEX campaign_deliveries_run_progress_idx;
DROP INDEX campaign_deliveries_reached_idx;
ALTER TABLE campaign_deliveries DROP CONSTRAINT campaign_deliveries_run_id_user_id_channel_key;
-- Later runs may have reached someone again, which the old key cannot hold.
DELETE FROM campaign_deliveries d USING campaign_runs r WHERE r.id = d.run_id AND r.number > 1;
ALTER TABLE campaign_deliveries ADD CONSTRAINT campaign_deliveries_campaign_id_user_id_channel_key UNIQUE (campaign_id, user_id, channel);
DROP INDEX delivery_attempts_run_response_idx;
ALTER TABLE delivery_attempts DROP COLUMN run_id;
ALTER TABLE campaign_deliveries DROP COLUMN run_id;
DROP TABLE campaign_runs;
