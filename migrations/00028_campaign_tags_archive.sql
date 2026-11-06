-- +goose Up

-- Tags group campaigns the way a team thinks of them. Archiving takes a campaign out
-- of the list but keeps its report; deleting removes a campaign that never sent from
-- the app. The audit trail is append-only, so a deleted campaign's row stays for it.
ALTER TABLE campaigns
    ADD COLUMN tags TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD COLUMN deleted_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE campaigns DROP COLUMN deleted_at, DROP COLUMN archived_at, DROP COLUMN tags;
