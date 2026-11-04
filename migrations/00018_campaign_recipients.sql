-- +goose Up
-- Who a campaign's approval covers. A one-time send goes to these people; a repeating
-- one finds who matches at each run. Older approvals kept no list and read the rule.
CREATE TABLE campaign_recipients (
    campaign_version_id UUID NOT NULL REFERENCES campaign_versions (id) ON DELETE CASCADE,
    user_id             BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (campaign_version_id, user_id)
);
ALTER TABLE campaign_versions ADD COLUMN recipients_kept BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE campaign_versions DROP COLUMN recipients_kept;
DROP TABLE campaign_recipients;
