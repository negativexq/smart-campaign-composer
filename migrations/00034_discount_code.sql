-- +goose Up

-- A discount code chosen on the form. A code the brief names counts when this is empty.
ALTER TABLE campaigns
    ADD COLUMN discount_code TEXT CHECK (discount_code ~ '^[A-Z0-9][A-Z0-9-]{2,29}$');

-- +goose Down

ALTER TABLE campaigns DROP COLUMN discount_code;
