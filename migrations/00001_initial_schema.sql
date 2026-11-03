-- +goose Up

CREATE TABLE users (
    id         BIGINT PRIMARY KEY,
    first_name TEXT NOT NULL,
    city       TEXT NOT NULL,
    email      TEXT NOT NULL UNIQUE,
    phone      TEXT NOT NULL UNIQUE,
    language   TEXT NOT NULL
);
CREATE INDEX users_city_idx ON users (city);

CREATE TABLE consents (
    user_id    BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    channel    TEXT NOT NULL CHECK (channel IN ('sms', 'email')),
    allowed    BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel)
);

CREATE TABLE cart_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    product_id  BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX cart_events_user_time_idx ON cart_events (user_id, occurred_at);
CREATE INDEX cart_events_time_idx ON cart_events (occurred_at);

CREATE TABLE purchase_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    product_id  BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX purchase_events_user_time_idx ON purchase_events (user_id, occurred_at);

CREATE TABLE campaigns (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    brief      TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN (
        'DRAFT', 'GENERATING', 'READY_FOR_REVIEW', 'GENERATION_FAILED', 'APPROVED',
        'DISPATCHING', 'COMPLETED', 'COMPLETED_WITH_FAILURES', 'CANCELLED'
    )),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE campaign_versions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id    UUID NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    version        INT NOT NULL,
    segment        JSONB NOT NULL,
    content        JSONB NOT NULL,
    llm_provider   TEXT,
    llm_model      TEXT,
    audience_as_of TIMESTAMPTZ,
    approved_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, version)
);

-- +goose StatementBegin
CREATE FUNCTION reject_approved_version_change() RETURNS trigger AS $$
BEGIN
    IF OLD.approved_at IS NOT NULL THEN
        RAISE EXCEPTION 'campaign version % is approved and cannot change', OLD.id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER campaign_versions_immutable_after_approval
    BEFORE UPDATE OR DELETE ON campaign_versions
    FOR EACH ROW EXECUTE FUNCTION reject_approved_version_change();

-- campaign_deliveries doubles as the durable send queue.
CREATE TABLE campaign_deliveries (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id         UUID NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    campaign_version_id UUID NOT NULL REFERENCES campaign_versions (id),
    user_id             BIGINT NOT NULL REFERENCES users (id),
    channel             TEXT NOT NULL CHECK (channel IN ('sms', 'email')),
    idempotency_key     TEXT NOT NULL UNIQUE,
    status              TEXT NOT NULL DEFAULT 'QUEUED' CHECK (status IN (
        'QUEUED', 'SENDING', 'SENT', 'RETRYING', 'FAILED', 'SKIPPED', 'UNKNOWN'
    )),
    attempts            INT NOT NULL DEFAULT 0,
    next_attempt_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    claim_token         UUID,
    lease_expires_at    TIMESTAMPTZ,
    provider_message_id TEXT,
    last_error          TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at             TIMESTAMPTZ,
    -- Scoped to the campaign rather than the version, so a re-approved version
    -- cannot message someone an earlier version already reached.
    UNIQUE (campaign_id, user_id, channel)
);
CREATE INDEX campaign_deliveries_claimable_idx ON campaign_deliveries (next_attempt_at)
    WHERE status IN ('QUEUED', 'RETRYING');
CREATE INDEX campaign_deliveries_lease_idx ON campaign_deliveries (lease_expires_at)
    WHERE status = 'SENDING';
CREATE INDEX campaign_deliveries_progress_idx ON campaign_deliveries (campaign_id, status);

CREATE TABLE delivery_attempts (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    delivery_id   UUID NOT NULL REFERENCES campaign_deliveries (id) ON DELETE CASCADE,
    attempt       INT NOT NULL,
    response_code INT,
    outcome       TEXT NOT NULL,
    error         TEXT,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (delivery_id, attempt)
);

-- +goose Down

DROP TABLE delivery_attempts;
DROP TABLE campaign_deliveries;
DROP TRIGGER campaign_versions_immutable_after_approval ON campaign_versions;
DROP FUNCTION reject_approved_version_change();
DROP TABLE campaign_versions;
DROP TABLE campaigns;
DROP TABLE purchase_events;
DROP TABLE cart_events;
DROP TABLE consents;
DROP TABLE users;
