package http

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// rejectNUL turns a NUL character in a request into a client error before
// it reaches a Postgres text parameter. PostgreSQL text cannot hold U+0000,
// and a statement that carries one aborts the surrounding transaction
// (SQLSTATE 22021, then 25P02 for the next statement), so without this guard
// a fuzzed or hostile request becomes a 500 on the real runtime (ADR 0039).
// The in-memory adapters never noticed, which is how it stayed hidden.
//
//   - a NUL in the URL path can never name a resource: 404, the same answer
//     as any other identifier that does not exist;
//   - a NUL in a JSON body (a raw byte or the \u0000 escape) is a malformed
//     request: 400 malformed-request-body, before the Idempotency-Key
//     middleware records anything.
func rejectNUL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.ContainsRune(r.URL.Path, 0) || strings.ContainsRune(r.URL.RawPath, 0) || strings.Contains(r.URL.RawPath, "%00") {
			writeProblem(w, http.StatusNotFound, problemInfo{"resource-not-found", "Resource not found"}, "no resource matches the requested path", r.URL.Path)
			return
		}
		if r.Body != nil && r.Body != http.NoBody {
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				writeProblem(w, http.StatusBadRequest, problemInfo{"malformed-request-body", "The request body could not be read"}, err.Error(), r.URL.Path)
				return
			}
			if containsNUL(body) {
				writeProblem(w, http.StatusBadRequest, problemInfo{"malformed-request-body", "The request body contains a NUL character"}, "a NUL character (U+0000) is not allowed in a request body", r.URL.Path)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

// containsNUL reports whether a JSON document carries U+0000, either as a raw
// 0x00 byte or as the six-character escape \u0000. A backslash that is itself
// escaped (\\u0000, a literal backslash followed by the text u0000) is not a
// NUL: the escape only counts when it is preceded by an even number of
// backslashes.
func containsNUL(body []byte) bool {
	if bytes.IndexByte(body, 0) >= 0 {
		return true
	}
	const escape = `\u0000`
	for from := 0; ; {
		i := bytes.Index(body[from:], []byte(escape))
		if i < 0 {
			return false
		}
		i += from
		slashes := 0
		for j := i - 1; j >= 0 && body[j] == '\\'; j-- {
			slashes++
		}
		if slashes%2 == 0 {
			return true
		}
		from = i + len(escape)
	}
}
