-- +goose Up
-- Remembers that the starter templates were added, so deleting one does not bring it back.
CREATE TABLE starter_content (
    only_row  BOOLEAN PRIMARY KEY DEFAULT true CHECK (only_row),
    loaded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE starter_content;
