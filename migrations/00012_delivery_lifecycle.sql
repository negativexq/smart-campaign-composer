-- +goose Up

-- A provider also reports whether an accepted message arrived.
ALTER TABLE delivery_events DROP CONSTRAINT delivery_events_event_type_check;
ALTER TABLE delivery_events ADD CONSTRAINT delivery_events_event_type_check
    CHECK (event_type IN ('delivered', 'bounced', 'opened'));

-- +goose Down

DELETE FROM delivery_events WHERE event_type <> 'opened';
ALTER TABLE delivery_events DROP CONSTRAINT delivery_events_event_type_check;
ALTER TABLE delivery_events ADD CONSTRAINT delivery_events_event_type_check
    CHECK (event_type IN ('opened'));
