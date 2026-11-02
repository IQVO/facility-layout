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

const (
	// IdempotencyKeyHeader is the request header a caller must supply on a
	// route wrapped by RequireIdempotencyKey.
	IdempotencyKeyHeader = "Idempotency-Key"

	// IdempotencyReplayedHeader is the response header set to "true" on every
	// replay of a stored answer, and only on a replay: the first execution of
	// a key never carries it (REST API guideline 2.3).
	IdempotencyReplayedHeader = "Idempotency-Replayed"

	// maxIdempotencyKeyLength is the longest accepted Idempotency-Key.
	maxIdempotencyKeyLength = 128
)

// validIdempotencyKey reports whether key is 1-128 printable ASCII characters.
func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// RequireIdempotencyKey is route-scoped (chi's r.With(...)) transactional
// idempotency-key middleware for a state-changing endpoint: every POST route
// of this service (see server.go's router wiring; deliberately NOT the whole
// router — PUT and DELETE are idempotent by method). docs/docs/adr has this
// middleware's ADR (header contract, the transactional design, the
// concurrency argument) and the REST guideline ADR for the contract it now
// implements:
//
//   - a missing key is 400 idempotency-key-required, a key that is not 1-128
//     printable ASCII characters is 400 idempotency-key-invalid;
//   - the key is GLOBAL (the stored row is looked up by key alone) and the
//     request fingerprint is method + path + SHA-256(body): the same key with
//     the same fingerprint replays the stored answer (status, body and
//     headers, plus Idempotency-Replayed: true), the same key with a
//     different fingerprint is 409 idempotency-key-reused;
//   - 2xx and 4xx answers are stored; a 5xx or a panic rolls the whole
//     transaction back, key row included, so the retry re-executes.
//
// Mirrors order-management's identically-named middleware (PR #105, ADR 0023).
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
					"This endpoint changes state and requires a caller-supplied "+IdempotencyKeyHeader+" header so a retried request is never applied twice.",
					r.URL.Path)
				return
			}
			if !validIdempotencyKey(key) {
				writeProblem(w, http.StatusBadRequest,
					problemInfo{"idempotency-key-invalid", "Idempotency-Key header is invalid"},
					"The "+IdempotencyKeyHeader+" header must be 1-128 printable ASCII characters (a client-generated UUID per logical submit).",
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

	var storedMethod, storedPath, storedHash string
	var statusCode *int
	var responseBody []byte
	var responseHeadersRaw []byte
	err := pool.QueryRow(ctx, `
		SELECT method, path, request_hash, status_code, response_body, response_headers
		FROM idempotency_keys WHERE key = $1
	`, key).Scan(&storedMethod, &storedPath, &storedHash, &statusCode, &responseBody, &responseHeadersRaw)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}

	// The fingerprint is method + path + SHA-256(body) (REST API guideline
	// 2.3): the same key on another route, or with another body, is a client
	// bug and NOT a retry. It is 409 — distinguishable from a business 409 by
	// the problem type — and it never executes anything.
	if storedMethod != r.Method || storedPath != r.URL.Path || storedHash != requestHash {
		writeProblem(w, http.StatusConflict,
			problemInfo{"idempotency-key-reused", "Idempotency-Key was already used with a different request"},
			"The Idempotency-Key header on this request was already used with a different method, path or body. "+
				"Use a new Idempotency-Key for a genuinely different request.",
			r.URL.Path)
		return
	}

	// The fingerprints match, so this row belongs to a PRIOR call with the
	// exact same key, method, path and body — a genuine retry.
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
	w.Header().Set(IdempotencyReplayedHeader, "true")
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
//
// The handler runs behind a SAVEPOINT. A mapped domain conflict -- a racing
// request losing on a unique index (23505 -> 409), say -- fails a statement
// on the server and leaves the transaction ABORTED even though the handler
// answers a truthful 4xx. Releasing the savepoint exposes that: the release
// fails, ROLLBACK TO SAVEPOINT discards the half-done work and heals the
// transaction, and the 4xx outcome is recorded and committed like any
// other. A 2xx answer behind a failed release is a lie (its writes are
// gone), so it rolls everything back and fails the request instead.
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

	if _, err := tx.Exec(ctx, "SAVEPOINT handler_run"); err != nil {
		_ = tx.Rollback(ctx)
		writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
		return
	}
	txCtx := pgtx.WithTx(ctx, tx)
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, r.WithContext(txCtx))

	// A server error is not a deterministic answer (guideline 2.5): roll the
	// whole transaction back — the key row with it — and answer the client,
	// so a retry with the same key executes again instead of replaying a 5xx.
	if rec.Code >= http.StatusInternalServerError {
		_ = tx.Rollback(ctx)
		writeRecorded(w, rec)
		return
	}

	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT handler_run"); err != nil {
		// A statement inside the handler failed on the server: the
		// transaction is aborted. Rolling back to the savepoint discards the
		// handler's partial writes -- the correct all-or-nothing semantics
		// for an error answer -- and returns the transaction to health.
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT handler_run"); rerr != nil {
			_ = tx.Rollback(ctx)
			writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
			return
		}
		if rec.Code < http.StatusBadRequest {
			// A success answer whose writes cannot be committed must neither
			// be returned nor cached: fail the request so the key retries.
			_ = tx.Rollback(ctx)
			writeProblem(w, http.StatusInternalServerError, internalErrorProblem, err.Error(), r.URL.Path)
			return
		}
	}

	// Every other NORMAL (non-panic) response is cached here, including a
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
	writeRecorded(w, rec)
}

// writeRecorded copies a captured response onto the real writer: headers,
// then status, then body.
func writeRecorded(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, vs := range rec.Header() {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
