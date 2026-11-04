-- +goose Up
-- A campaign that made no progress through a long provider outage stops until someone resumes it.
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'DRAFT', 'GENERATING', 'READY_FOR_REVIEW', 'GENERATION_FAILED', 'APPROVED', 'SCHEDULED',
    'DISPATCHING', 'PAUSED', 'COMPLETED', 'COMPLETED_WITH_FAILURES', 'CANCELLED'
));

-- +goose Down
UPDATE campaigns SET status = 'DISPATCHING' WHERE status = 'PAUSED';
ALTER TABLE campaigns DROP CONSTRAINT campaigns_status_check;
ALTER TABLE campaigns ADD CONSTRAINT campaigns_status_check CHECK (status IN (
    'DRAFT', 'GENERATING', 'READY_FOR_REVIEW', 'GENERATION_FAILED', 'APPROVED', 'SCHEDULED',
    'DISPATCHING', 'COMPLETED', 'COMPLETED_WITH_FAILURES', 'CANCELLED'
));
