package http

import "net/http"

// registered collects the problem types declared with register below. The typed domain errors are
// not here: they are the rows of errorCategories (errors.go), their status is the row's.
var registered []problemType

// register declares a problem type that is not a typed domain error: it records the entry for
// allProblemTypes and returns the problemInfo to answer it with. The status is the one the call
// sites answer; the replay-class tests use it to drive the real error writer.
func register(slug, title string, status int, class replayClass) problemInfo {
	info := problemInfo{slug, title, class}
	registered = append(registered, problemType{Slug: slug, Status: status, Class: class, info: info})
	return info
}

// The problem types that are not a typed domain error. Each is declared with register, which
// takes the replay class, so none can exist without one (replay_class_test.go scans for a
// problemInfo literal anywhere else).
//
// Request validation is FINAL: the answer is a function of the request alone. A refusal that
// depends on state, a dependency or the server itself is RETRYABLE (a 5xx is never stored).
var (
	problemMalformedJSON  = register(slugMalformedRequestBody, "The request body is not valid JSON", http.StatusBadRequest, classFinal)
	problemBodyUnreadable = register(slugMalformedRequestBody, "The request body could not be read", http.StatusBadRequest, classFinal)
	problemBodyHasNUL     = register(slugMalformedRequestBody, "The request body contains a NUL character", http.StatusBadRequest, classFinal)

	problemKeyRequired = register("idempotency-key-required", "Idempotency-Key header is required", http.StatusBadRequest, classFinal)
	problemKeyInvalid  = register("idempotency-key-invalid", "Idempotency-Key header is invalid", http.StatusBadRequest, classFinal)
	problemKeyReused   = register("idempotency-key-reused", "Idempotency-Key was already used with a different request", http.StatusConflict, classFinal)

	problemResourceNotFound = register("resource-not-found", "Resource not found", http.StatusNotFound, classFinal)

	problemInvalidLayoutFormat   = register("invalid-layout-format", "Layout format must be json or svg", http.StatusBadRequest, classFinal)
	problemUnknownQueryParameter = register("unknown-query-parameter", "Unknown query parameter", http.StatusBadRequest, classFinal)
	problemInvalidLimit          = register("invalid-limit", "Invalid limit", http.StatusBadRequest, classFinal)
	problemInvalidCursor         = register("invalid-cursor", "Invalid cursor", http.StatusBadRequest, classFinal)
	problemInvalidReportQuery    = register("invalid-report-query", "The report query is malformed or missing a required parameter", http.StatusBadRequest, classFinal)

	problemInternalError         = register("internal-error", "An unexpected internal error occurred", http.StatusInternalServerError, classRetryable)
	problemReportStoreError      = register("report-store-error", "The report could not be served", http.StatusInternalServerError, classRetryable)
	problemDependencyUnavailable = register("dependency-unavailable", "A runtime dependency is unavailable", http.StatusServiceUnavailable, classRetryable)
)
