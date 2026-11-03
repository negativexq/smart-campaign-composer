-- +goose Up

-- Who did it. Earlier rows stay 'unknown' rather than being credited to anyone.
-- Work the system does by itself uses the default, so a person must be stated.
ALTER TABLE audit_events
    ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'unknown' CHECK (actor_type IN ('user', 'system', 'unknown')),
    ADD COLUMN actor TEXT;
ALTER TABLE audit_events ALTER COLUMN actor_type SET DEFAULT 'system';

ALTER TABLE campaigns ADD COLUMN created_by TEXT;
-- A scheduled run is started by the system, on behalf of whoever set it up.
ALTER TABLE campaign_schedules ADD COLUMN created_by TEXT;

-- +goose Down

ALTER TABLE campaign_schedules DROP COLUMN created_by;
ALTER TABLE campaigns DROP COLUMN created_by;
ALTER TABLE audit_events DROP COLUMN actor, DROP COLUMN actor_type;
