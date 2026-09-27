-- Idempotency-Key middleware (see docs/docs/adr/0019-idempotency-key-middleware.md).
--
-- One row per Idempotency-Key value ever seen on a protected route. The
-- middleware INSERTs a bare row (status_code/response_body/response_headers
-- all NULL) inside its own transaction before calling the real handler,
-- then UPDATEs that same row with the outcome and commits — all in ONE
-- transaction shared with the use case's own aggregate write (see
-- unit_of_work.go's querierFrom / internal/pgtx, reused unchanged here).
-- A reader can therefore never observe a COMMITTED row with a NULL
-- status_code: either the row was never committed at all (the inserting
-- transaction rolled back), or it was committed only after the UPDATE
-- populated every outcome column. See idempotency.go's doc comment for
-- the full argument.
CREATE TABLE idempotency_keys (
    key              TEXT PRIMARY KEY,
    method           TEXT NOT NULL,
    path             TEXT NOT NULL,
    request_hash     TEXT NOT NULL,
    status_code      INTEGER,
    response_body    BYTEA,
    response_headers JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at     TIMESTAMPTZ
);

-- No query in this service filters/orders by created_at today; the index
-- exists solely so a future cleanup/TTL job (explicitly deferred — see
-- the ADR's "Known follow-up" section) can scan old rows without a full
-- table scan, mirroring outbox_events' own unpublished-rows index in
-- spirit (an index anticipated by a known, named follow-up, not spec
-- work happening now).
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
