-- +goose Up
-- A marketer's own email design: choices, never markup. A campaign keeps its own copy, so
-- changing or removing a design here does not touch anything already written with it.
CREATE TABLE email_designs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    spec       JSONB NOT NULL,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX email_designs_name_idx ON email_designs (lower(name));

ALTER TABLE campaigns ADD COLUMN email_design JSONB;

-- +goose Down
ALTER TABLE campaigns DROP COLUMN email_design;
DROP TABLE email_designs;
