-- +goose Up
-- What is known about an uploaded image. The file itself lives in the asset store.
CREATE TABLE assets (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    mime       TEXT NOT NULL,
    bytes      INTEGER NOT NULL,
    width      INTEGER NOT NULL,
    height     INTEGER NOT NULL,
    file_name  TEXT NOT NULL UNIQUE,
    created_by TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The one set of brand defaults new templates start from.
CREATE TABLE brand_profile (
    only_row   BOOLEAN PRIMARY KEY DEFAULT true CHECK (only_row),
    spec       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE brand_profile;
DROP TABLE assets;
