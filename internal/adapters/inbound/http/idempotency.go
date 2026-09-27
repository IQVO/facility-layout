package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/facility-layout/internal/pgtx"
)

// IdempotencyKeyHeader is the request header a caller must supply on a
// route wrapped by RequireIdempotencyKey.
const IdempotencyKeyHeader = "Idempotency-Key"

// RequireIdempotencyKey is route-scoped (chi's r.With(...)) transactional
// idempotency-key middleware for a true resource-CREATION endpoint. See
// server.go's router wiring for the exact list of routes it is applied
// to (deliberately NOT the whole router), and docs/docs/adr for this
// middleware's ADR (header contract, the transactional design, the
// concurrency argument, and the deliberate v1 scope, plus which
// resource-creation endpoints were deliberately excluded and why).
// Mirrors order-management's identically-named middleware verbatim
// (PR #105, ADR 0023) — the reference implementation this fleet's other
// services copy for this feature.
//
// # The core correctness argument: a committed row's status_code is NEVER NULL
//
// idempotency_keys.status_code (and response_body/response_headers) start
// NULL on insert and are only ever set by the SAME transaction that
// inserted the row, via one UPDATE, immediately before that transaction
// commits (see the "fresh key" branch below). There is no third state,
// no "in progress" marker, no timeout, no 409-retry-later branch: a row
// is either
//
//  1. not committed at all (the inserting transaction rolled back — e.g.
//     the wrapped handler panicked, or the UPDATE/commit itself failed),
//     in which case no OTHER transaction can ever see it (Postgres never
//     exposes an uncommitted row to another session under any isolation
//     level this pool uses), or
//  2. committed, in which case status_code/response_body/response_headers
//     were ALREADY populated by the UPDATE that ran, in the same
//     transaction, before the COMMIT that made the row visible at all.
//
// So a reader that successfully finds a row (case 2 is the only case a
// reader ever observes) can rely on status_code being non-NULL with no
// polling, no waiting, and no "come back later" response — a real,
// simpler alternative to naive two-phase idempotency-key designs that
// need an explicit in-progress state and a client-facing retry-after.
//
// # The concurrency argument: Postgres' own unique-index lock does the serialization
//
// Two concurrent requests with the SAME key race on
// `INSERT ... ON CONFLICT (key) DO NOTHING`. Postgres serializes them at
// the primary-key unique index: the SECOND (and every later) inserter's
// statement BLOCKS until the FIRST inserter's transaction resolves
// (commit OR rollback) — this is standard Postgres row-lock-on-conflicting-
// insert behaviour, not an assumption this code makes. So by the time ANY
// transaction observes rowsAffected()==0 on this INSERT, the ORIGINAL
// inserting transaction has unconditionally finished. Combined with the
// no-null-status-code invariant above: whenever this middleware takes the
// "0 rows" branch, the existing row — if the original transaction
// committed — already has its outcome fully populated, never half-written;
// if the original transaction rolled back, the row does not exist at all
// and THIS call's own blocked INSERT succeeds instead (rowsAffected()==1,
// the "fresh key" branch), because a rolled-back insert leaves nothing
// behind to conflict with. Either way, no polling loop, no lock-retry
// budget, and no timeout are needed — the database's own MVCC/locking
// semantics ARE the synchronization primitive.
func RequireIdempotencyKey(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" {
				writeProblem(w, http.StatusBadRequest,
					problemInfo{"idempotency-key-required", "Idempotency-Key header is required"},
					"This endpoint creates a new resource on every call and requires a caller-supplied "+IdempotencyKeyHeader+" header so a retried request is never applied twice.",
					r.URL.Path)
				return
			}

			// The body is read into memory once, HASHED, and then restored
			// (io.NopCloser over a bytes.Reader) so downstream JSON decoding
			// in the real handler is completely unaffected — it sees the
			// exact same bytes it would have without this middleware.
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				writeProblem(w, http.StatusBadRequest,
					problemInfo{"malformed-request-body", "The request body could not be read"},
					err.Error(), r.URL.Path)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			sum := sha256.Sum256(bodyBytes)
			requestHash := hex.EncodeToString(sum[:])

			ctx := r.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
				return
			}

			tag, err := tx.Exec(ctx, `
				INSERT INTO idempotency_keys (key, method, path, request_hash)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (key) DO NOTHING
			`, key, r.Method, r.URL.Path, requestHash)
			if err != nil {
				_ = tx.Rollback(ctx)
				writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
				return
			}

			if tag.RowsAffected() == 0 {
				// A row already exists — see the type doc's concurrency
				// argument for why the transaction that inserted it is
				// GUARANTEED to have already resolved by this point. This
				// call's own transaction never wrote anything, so it is
				// rolled back before the plain, non-transactional read.
				_ = tx.Rollback(ctx)
				replayCachedResponse(w, r, pool, key, requestHash)
				return
			}

			runFreshRequest(w, r, ctx, tx, key, next)
		})
	}
}

// internalErrorProblem mirrors problemFor's own default case — this
// middleware runs before any domain/application error exists to map, so
// it constructs the same category directly rather than routing through
// problemFor.
var internalErrorProblem = problemInfo{"internal-error", "An unexpected internal error occurred"}

// storedResponseHeaders is the JSON wire shape written to
// idempotency_keys.response_headers — a plain encoding of http.Header
// (map[string][]string), so no information (multi-value headers
// included) is lost on the round trip through JSONB.
type storedResponseHeaders = http.Header

// replayCachedResponse handles the "a row already exists for this key"
// path. Per RequireIdempotencyKey's own doc comment, the transaction that
// originally inserted this key has definitely already resolved — commit
// or rollback — by the time this function runs, so a plain read (no
// transaction of its own) is sufficient and cannot race a half-written
// row.
func replayCachedResponse(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, key, requestHash string) {
	ctx := r.Context()

	var storedHash string
	var statusCode *int
	var responseBody []byte
	var responseHeadersRaw []byte
	err := pool.QueryRow(ctx, `
		SELECT request_hash, status_code, response_body, response_headers
		FROM idempotency_keys WHERE key = $1
	`, key).Scan(&storedHash, &statusCode, &responseBody, &responseHeadersRaw)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}

	if storedHash != requestHash {
		writeProblem(w, http.StatusUnprocessableEntity,
			problemInfo{"idempotency-key-reused", "Idempotency-Key was already used with a different request"},
			"The Idempotency-Key header on this request was already used with a request that had a different body. "+
				"Use a new Idempotency-Key for a genuinely different request.",
			r.URL.Path)
		return
	}

	// storedHash == requestHash, so this row belongs to a PRIOR call with
	// the exact same key AND the exact same body — a genuine retry.
	// status_code is guaranteed non-NULL here (see the type doc's
	// no-null-status-code-ever-committed invariant): this row could only
	// have become visible to this plain read via a transaction that
	// populated it before committing.
	if statusCode == nil {
		// Unreachable given the invariant above; a defensive 500 rather
		// than a panic or a fabricated response if it is ever violated.
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem,
			"idempotency row committed with no recorded outcome — this should be impossible", r.URL.Path)
		return
	}

	if len(responseHeadersRaw) > 0 {
		var headers storedResponseHeaders
		if err := json.Unmarshal(responseHeadersRaw, &headers); err == nil {
			for k, vs := range headers {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
		}
	}
	w.WriteHeader(*statusCode)
	_, _ = w.Write(responseBody)
}

// runFreshRequest handles the "genuinely new key" path: it calls next
// with a CAPTURING recorder (rather than the real http.ResponseWriter),
// persists the outcome and commits, and ONLY THEN copies the captured
// response onto the real http.ResponseWriter. This ordering — capture,
// persist+commit, THEN write to the real client — is what guarantees the
// no-null-status-code-ever-committed invariant documented on
// RequireIdempotencyKey: nothing observable to another transaction
// happens before the outcome is fully known and durably recorded.
func runFreshRequest(w http.ResponseWriter, r *http.Request, ctx context.Context, tx pgx.Tx, key string, next http.Handler) {
	// A panic from the wrapped handler must roll back this transaction —
	// so neither the idempotency row nor any domain write it made
	// visible-if-committed (the aggregate Save, the outbox insert — see
	// the tx-join mechanism this ctx now carries) survives — and then
	// re-panic so the outer chi Recoverer middleware still produces the
	// service's normal 500 response. A panic's outcome is deliberately
	// never cached: it is not a "normal" response (see the type doc), so
	// a retry after a panic must re-attempt the real work, not replay a
	// stored 500.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	txCtx := pgtx.WithTx(ctx, tx)
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, r.WithContext(txCtx))

	// Every NORMAL (non-panic) response is cached here, including a
	// business-logic error response (4xx from validation, etc.) — a
	// deliberate v1 simplification (see the ADR): a client retrying the
	// exact same key+body deterministically gets the exact same answer,
	// including a validation error. A caller wanting a different outcome
	// must use a new Idempotency-Key.
	headersJSON, err := json.Marshal(rec.Header())
	if err != nil {
		_ = tx.Rollback(ctx)
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}

	if _, err := tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET status_code = $1, response_body = $2, response_headers = $3, completed_at = now()
		WHERE key = $4
	`, rec.Code, rec.Body.Bytes(), headersJSON, key); err != nil {
		_ = tx.Rollback(ctx)
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}

	// Only after a successful commit does the real client see anything —
	// the recorder's headers, then status, then body, VERBATIM.
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
