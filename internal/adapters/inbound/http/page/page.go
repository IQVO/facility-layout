// Package page is the fleet reference for REST keyset pagination (REST API guideline section 3).
// Copy this file verbatim into the service (e.g. internal/adapters/inbound/http/page/page.go) and
// change only the package path. Do not invent a second cursor format.
package page

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
)

const (
	// DefaultLimit and MaxLimit are the fleet-wide bounds for the `limit` query parameter.
	DefaultLimit = 100
	MaxLimit     = 500
)

// ErrInvalidLimit and ErrInvalidCursor map to 400 problems `invalid-limit` and `invalid-cursor`.
var (
	ErrInvalidLimit  = errors.New("limit must be an integer between 1 and 500")
	ErrInvalidCursor = errors.New("cursor is not a valid page token")
)

// Page is the ONLY collection response shape: { "items": [...], "nextCursor": "..." }.
// NextCursor is omitted on the last page. Items is never null.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// Position is the keyset a cursor points after: the last returned row's sort key and id.
// Repositories order by (Sort, ID) so ties never skip or repeat a row.
type Position struct {
	Sort string `json:"s"`
	ID   string `json:"i"`
}

// Unknown returns the first query parameter of r that the collection does not declare
// (`limit` and `cursor` are always declared; allowed lists the filters), or "" when every
// parameter is known. A collection GET answers 400 `unknown-query-parameter` for it: a
// consumer still sending a retired paging parameter (`after`, `from`, `offset`, `nextToken`)
// then fails loudly instead of silently reading page one forever. Names are checked in
// sorted order so the error is deterministic.
func Unknown(r *http.Request, allowed ...string) string {
	known := map[string]bool{"limit": true, "cursor": true}
	for _, a := range allowed {
		known[a] = true
	}
	var names []string
	for name := range r.URL.Query() {
		if !known[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// Limit parses `limit` from the query string. Absent -> DefaultLimit. A `limit` that is
// present but empty, or repeated, is ErrInvalidLimit: the parameter is a single integer, and
// silently taking the first value would accept a request the contract says is invalid.
func Limit(r *http.Request) (int, error) {
	vals, present := r.URL.Query()["limit"]
	if !present {
		return DefaultLimit, nil
	}
	if len(vals) != 1 || vals[0] == "" {
		return 0, ErrInvalidLimit
	}
	n, err := strconv.Atoi(vals[0])
	if err != nil || n < 1 || n > MaxLimit {
		return 0, ErrInvalidLimit
	}
	return n, nil
}

// After parses `cursor` from the query string. Absent (or empty) -> nil (first page). A
// repeated `cursor` is ErrInvalidCursor for the same reason a repeated `limit` is invalid.
func After(r *http.Request) (*Position, error) {
	vals := r.URL.Query()["cursor"]
	if len(vals) > 1 {
		return nil, ErrInvalidCursor
	}
	if len(vals) == 0 || vals[0] == "" {
		return nil, nil
	}
	return Decode(vals[0])
}

// Encode makes the opaque cursor for a position.
func Encode(p Position) string {
	b, _ := json.Marshal(p) // a struct of two strings cannot fail to marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

// Decode parses an opaque cursor; any malformed value is ErrInvalidCursor.
func Decode(s string) (*Position, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var p Position
	if err := json.Unmarshal(b, &p); err != nil || p.ID == "" {
		return nil, ErrInvalidCursor
	}
	return &p, nil
}

// Of builds the response page from rows fetched with `limit+1` (ask the repository for
// FetchSize(limit) rows). When more than `limit` rows came back there is a next page and
// the cursor points after the last row that is returned.
func Of[T any](rows []T, limit int, at func(T) Position) Page[T] {
	if rows == nil {
		rows = []T{}
	}
	if len(rows) <= limit {
		return Page[T]{Items: rows}
	}
	rows = rows[:limit]
	return Page[T]{Items: rows, NextCursor: Encode(at(rows[len(rows)-1]))}
}

// FetchSize is how many rows a repository must request to know whether another page exists.
func FetchSize(limit int) int { return limit + 1 }
