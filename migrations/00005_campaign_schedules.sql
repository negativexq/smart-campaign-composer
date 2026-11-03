-- +goose Up

ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'DRAFT', 'GENERATING', 'READY_FOR_REVIEW', 'GENERATION_FAILED', 'APPROVED', 'SCHEDULED',
    'DISPATCHING', 'COMPLETED', 'COMPLETED_WITH_FAILURES', 'CANCELLED'
));

-- next_run_at is NULL once a schedule has nothing left to do.
CREATE TABLE campaign_schedules (
    campaign_id UUID PRIMARY KEY REFERENCES campaigns (id) ON DELETE CASCADE,
    spec        JSONB NOT NULL,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    runs        INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX campaign_schedules_due_idx ON campaign_schedules (next_run_at) WHERE next_run_at IS NOT NULL;

-- +goose Down

DROP TABLE campaign_schedules;
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'DRAFT', 'GENERATING', 'READY_FOR_REVIEW', 'GENERATION_FAILED', 'APPROVED',
    'DISPATCHING', 'COMPLETED', 'COMPLETED_WITH_FAILURES', 'CANCELLED'
));
