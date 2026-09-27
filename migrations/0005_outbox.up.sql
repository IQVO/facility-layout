-- ADR-0018: transactional outbox. The old `events` table
-- (postgres.EventPublisher) appended every domain event with no relay
-- ever draining it — a dead end. This migration retires it and replaces
-- it with `outbox_events`: each row is one already-encoded Kafka message
-- (facility-layout has two topics, integration and analytics, so ONE
-- domain event fans out to up to two rows). A background relay drains
-- unpublished rows onto Kafka in the same order they were enqueued, so
-- the store and the topic can never diverge the way a bare
-- Save-then-Publish would allow.
DROP TABLE IF EXISTS events;

CREATE TABLE outbox_events (
    id           BIGSERIAL PRIMARY KEY,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    key          BYTEA,
    value        BYTEA       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT
);

-- The relay only ever asks "what is still unpublished, oldest first"; a
-- partial index keeps that scan tiny no matter how much published history
-- accumulates.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;
