-- +goose Up

-- What a provider reports after accepting a message. A delivery attempt is our
-- call to the provider; an event is the provider telling us what happened next.
CREATE TABLE delivery_events (
    delivery_id UUID NOT NULL REFERENCES campaign_deliveries (id) ON DELETE CASCADE,
    campaign_id UUID NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL CHECK (event_type IN ('opened')),
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Webhooks are repeated, so the first report of an event is the one kept.
    PRIMARY KEY (delivery_id, event_type)
);

-- +goose Down

DROP TABLE delivery_events;
