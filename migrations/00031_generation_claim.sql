-- +goose Up

-- Who holds a campaign's GENERATING status. A claim left for five minutes can be
-- taken over; the token tells the run that holds it now from one that lost it, so a
-- run still alive after a takeover cannot save, fail or keep the newer claim alive.
ALTER TABLE campaigns ADD COLUMN generation_claim UUID;
UPDATE campaigns SET generation_claim = gen_random_uuid() WHERE status = 'GENERATING';
ALTER TABLE campaigns ADD CONSTRAINT campaigns_generation_claim_while_generating
    CHECK ((generation_claim IS NOT NULL) = (status = 'GENERATING'));

-- +goose Down

ALTER TABLE campaigns DROP CONSTRAINT campaigns_generation_claim_while_generating;
ALTER TABLE campaigns DROP COLUMN generation_claim;
